package llamacpp

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/llm"
)

var (
	ErrInvalidOptions = errors.New("некорректные параметры локальной модели")
	ErrInvalidRequest = errors.New("некорректный запрос локальной модели")
	ErrContextLimit   = errors.New("запрос превышает контекст модели")
	ErrInvalidOutput  = errors.New("модель вернула некорректный текст")
	ErrBusy           = errors.New("локальная модель занята")
	ErrClosed         = errors.New("локальная модель закрыта")
	ErrGPUUnavailable = errors.New("вычислительный модуль GPU недоступен")
)

type Options struct {
	ModelPath   string
	LibraryDir  string
	ContextSize int
	GPULayers   int
	Threads     int
	Timeout     time.Duration
}

type Generator struct {
	backend tokenBackend
	timeout time.Duration
	mu      sync.Mutex
	closed  bool
}

type tokenPiece struct {
	Bytes []byte
	End   bool
}

type tokenBackend interface {
	Template() string
	Begin(context.Context, string, llm.Options) error
	Next(context.Context) (tokenPiece, error)
	End() error
	Close() error
}

var _ llm.Generator = (*Generator)(nil)

func Open(ctx context.Context, options Options) (*Generator, error) {
	if ctx == nil {
		return nil, fmt.Errorf("открыть локальную модель: %w", ErrInvalidRequest)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateOptions(options); err != nil {
		return nil, err
	}
	return openNative(ctx, options)
}

func validateOptions(options Options) error {
	if options.ModelPath == "" || containsNUL(options.ModelPath) {
		return fmt.Errorf("%w: путь к модели обязателен и не должен содержать NUL", ErrInvalidOptions)
	}
	if options.LibraryDir == "" || containsNUL(options.LibraryDir) {
		return fmt.Errorf("%w: каталог нативных библиотек обязателен и не должен содержать NUL", ErrInvalidOptions)
	}
	if options.ContextSize <= 0 || int64(options.ContextSize) > int64(^uint32(0)>>1) {
		return fmt.Errorf("%w: размер контекста должен быть в диапазоне [1, MaxInt32]", ErrInvalidOptions)
	}
	if options.GPULayers < 0 || int64(options.GPULayers) > int64(^uint32(0)>>1) {
		return fmt.Errorf("%w: число слоёв GPU должно быть в диапазоне [0, MaxInt32]", ErrInvalidOptions)
	}
	if options.Threads <= 0 || int64(options.Threads) > int64(^uint32(0)>>1) {
		return fmt.Errorf("%w: число потоков должно быть в диапазоне [1, MaxInt32]", ErrInvalidOptions)
	}
	if options.Timeout <= 0 {
		return fmt.Errorf("%w: время ожидания должно быть положительным", ErrInvalidOptions)
	}
	return nil
}

func newGenerator(backend tokenBackend, timeout time.Duration) (*Generator, error) {
	if backend == nil || isNilBackend(backend) {
		return nil, fmt.Errorf("%w: адаптер токенизатора обязателен", ErrInvalidOptions)
	}
	if timeout <= 0 {
		return nil, fmt.Errorf("%w: время ожидания должно быть положительным", ErrInvalidOptions)
	}
	return &Generator{backend: backend, timeout: timeout}, nil
}

func isNilBackend(backend tokenBackend) bool {
	value := reflect.ValueOf(backend)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func (g *Generator) Generate(ctx context.Context, request llm.Request, emit llm.Emit) (resultErr error) {
	if ctx == nil {
		return fmt.Errorf("%w: контекст обязателен", ErrInvalidRequest)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !g.mu.TryLock() {
		return ErrBusy
	}
	defer g.mu.Unlock()
	if g.closed {
		return ErrClosed
	}
	if emit == nil {
		return fmt.Errorf("%w: функция передачи дельт обязательна", ErrInvalidRequest)
	}
	if err := request.Options.Validate(); err != nil {
		return err
	}
	prompt, err := renderPrompt(g.backend.Template(), request.Dialogue)
	if err != nil {
		return err
	}
	requestCtx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()
	if err := requestCtx.Err(); err != nil {
		return err
	}
	endAttempted := true
	defer func() {
		if !endAttempted {
			return
		}
		if err := g.backend.End(); err != nil {
			endErr := fmt.Errorf("завершить нативный запрос: %w", err)
			closeErr := g.backend.Close()
			g.closed = true
			if closeErr != nil {
				resultErr = errors.Join(resultErr, endErr, fmt.Errorf("закрыть нативный вычислительный модуль: %w", closeErr))
			} else {
				resultErr = errors.Join(resultErr, endErr)
			}
		}
	}()
	if err := g.backend.Begin(requestCtx, prompt, request.Options); err != nil {
		return fmt.Errorf("подготовить нативный запрос: %w", err)
	}
	decoder := textDecoder{}
	requestEmit := func(delta llm.TextDelta) error {
		if err := requestCtx.Err(); err != nil {
			return err
		}
		return emit(delta)
	}
	for range request.Options.MaxTokens {
		if err := requestCtx.Err(); err != nil {
			return err
		}
		piece, err := g.backend.Next(requestCtx)
		if err != nil {
			if ctxErr := requestCtx.Err(); ctxErr != nil {
				return ctxErr
			}
			return fmt.Errorf("получить нативный токен: %w", err)
		}
		if err := requestCtx.Err(); err != nil {
			return err
		}
		if piece.End {
			if err := decoder.Finish(requestEmit); err != nil {
				return err
			}
			return requestCtx.Err()
		}
		if err := requestCtx.Err(); err != nil {
			return err
		}
		if err := decoder.Push(piece.Bytes, requestEmit); err != nil {
			return fmt.Errorf("передать текстовую дельту: %w", err)
		}
	}
	if err := requestCtx.Err(); err != nil {
		return err
	}
	if err := decoder.Finish(requestEmit); err != nil {
		return err
	}
	return requestCtx.Err()
}

func (g *Generator) Close() error {
	if !g.mu.TryLock() {
		return ErrBusy
	}
	defer g.mu.Unlock()
	if g.closed {
		return nil
	}
	if err := g.backend.Close(); err != nil {
		g.closed = true
		return fmt.Errorf("закрыть нативный вычислительный модуль: %w", err)
	}
	g.closed = true
	return nil
}

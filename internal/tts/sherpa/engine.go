package sherpa

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/audio"
	"github.com/Seraf-seraf/voice_assistent/internal/tts"
)

const (
	minThreads             = 1
	maxThreads             = 32
	ttsSampleRate          = 22050
	modelFileName          = "ru_RU-ruslan-medium.onnx"
	tokensFileName         = "tokens.txt"
	espeakDataDirName      = "espeak-ng-data"
	modelSHA256            = "43d3e034b04abe67c9ceab09a062617260e71804f55779cc68979a7b3e434064"
	maxPhraseAudioDuration = time.Minute
)

var (
	ErrInvalidOptions      = errors.New("некорректные параметры sherpa TTS")
	ErrUnavailable         = errors.New("модель sherpa TTS недоступна")
	ErrClosed              = errors.New("движок синтеза sherpa TTS закрыт")
	ErrSynthesis           = errors.New("ошибка синтеза sherpa TTS")
	ErrInvalidAudio        = errors.New("некорректный аудиорезультат sherpa TTS")
	ErrUnsupportedPlatform = errors.New("sherpa TTS не поддерживается на этой платформе")
)

type Options struct {
	ModelDir string
	Threads  int
}

func (o Options) Validate() error {
	if strings.TrimSpace(o.ModelDir) == "" || strings.ContainsRune(o.ModelDir, '\x00') {
		return fmt.Errorf("%w: путь к модели должен быть непустым и не содержать NUL", ErrInvalidOptions)
	}
	if o.Threads < minThreads || o.Threads > maxThreads {
		return fmt.Errorf("%w: число потоков должно быть в диапазоне [%d, %d]", ErrInvalidOptions, minThreads, maxThreads)
	}
	return nil
}

type backend interface {
	synthesize(context.Context, string) (audio.PCM, error)
	close() error
}

type Engine struct {
	backend backend
	closed  bool
}

func newEngine(backend backend) *Engine {
	return &Engine{backend: backend}
}

func openEngine(ctx context.Context, options Options) (*Engine, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}
	if err := preflightAssets(options); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	nativeBackend, err := openNativeBackend(ctx, options)
	if err != nil {
		return nil, err
	}
	if nativeBackend == nil {
		return nil, fmt.Errorf("%w: нативный конструктор вернул пустой результат", ErrUnavailable)
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(err, nativeBackend.close())
	}
	return newEngine(nativeBackend), nil
}

func (e *Engine) Synthesize(ctx context.Context, text string) (audio.PCM, error) {
	if e.closed {
		return audio.PCM{}, ErrClosed
	}
	if err := tts.ValidateText(text); err != nil {
		return audio.PCM{}, err
	}
	if err := ctx.Err(); err != nil {
		return audio.PCM{}, err
	}
	pcm, err := e.backend.synthesize(ctx, text)
	if ctxErr := ctx.Err(); ctxErr != nil {
		if err != nil {
			return audio.PCM{}, errors.Join(ctxErr, fmt.Errorf("%w: вызов синтезатора: %w", ErrSynthesis, err))
		}
		return audio.PCM{}, ctxErr
	}
	if err != nil {
		return audio.PCM{}, fmt.Errorf("%w: вызов синтезатора: %w", ErrSynthesis, err)
	}
	if len(pcm.Samples) == 0 {
		return audio.PCM{}, fmt.Errorf("%w: синтезатор вернул пустой результат", ErrSynthesis)
	}
	if pcm.SampleRate != ttsSampleRate {
		return audio.PCM{}, fmt.Errorf("%w: частота дискретизации %d, ожидалась %d", ErrInvalidAudio, pcm.SampleRate, ttsSampleRate)
	}
	if len(pcm.Samples) > ttsSampleRate*int(maxPhraseAudioDuration/time.Second) {
		return audio.PCM{}, fmt.Errorf("%w: длительность превышает %s", ErrInvalidAudio, maxPhraseAudioDuration)
	}
	if err := pcm.Validate(); err != nil {
		return audio.PCM{}, fmt.Errorf("%w: %w", ErrInvalidAudio, err)
	}
	pcm.Samples = append([]float32(nil), pcm.Samples...)
	return pcm, nil
}

func (e *Engine) Close() error {
	if e.closed {
		return nil
	}
	e.closed = true
	if err := e.backend.close(); err != nil {
		return fmt.Errorf("закрыть движок синтеза sherpa TTS: %w", err)
	}
	return nil
}

func preflightAssets(options Options) error {
	if err := options.Validate(); err != nil {
		return err
	}
	modelPath := filepath.Join(options.ModelDir, modelFileName)
	if err := checkReadableFile(modelPath); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrUnavailable, modelFileName, err)
	}
	model, err := os.Open(modelPath)
	if err != nil {
		return fmt.Errorf("%w: открыть %s: %w", ErrUnavailable, modelFileName, err)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, model)
	closeErr := model.Close()
	if copyErr != nil || closeErr != nil {
		return fmt.Errorf("%w: прочитать %s: %w", ErrUnavailable, modelFileName, errors.Join(copyErr, closeErr))
	}
	if got := fmt.Sprintf("%x", hash.Sum(nil)); got != modelSHA256 {
		return fmt.Errorf("%w: SHA-256 модели не совпадает с закреплённым значением", ErrUnavailable)
	}
	if err := checkReadableFile(filepath.Join(options.ModelDir, tokensFileName)); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrUnavailable, tokensFileName, err)
	}
	dataPath := filepath.Join(options.ModelDir, espeakDataDirName)
	data, err := os.Open(dataPath)
	if err != nil {
		return fmt.Errorf("%w: открыть %s: %w", ErrUnavailable, espeakDataDirName, err)
	}
	entries, readErr := data.ReadDir(1)
	closeErr = data.Close()
	if readErr != nil || closeErr != nil {
		return fmt.Errorf("%w: прочитать %s: %w", ErrUnavailable, espeakDataDirName, errors.Join(readErr, closeErr))
	}
	if len(entries) == 0 {
		return fmt.Errorf("%w: каталог %s пуст", ErrUnavailable, espeakDataDirName)
	}
	return nil
}

func checkReadableFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	info, statErr := file.Stat()
	closeErr := file.Close()
	if statErr != nil || closeErr != nil {
		return errors.Join(statErr, closeErr)
	}
	if !info.Mode().IsRegular() {
		return errors.New("ожидался обычный файл")
	}
	return nil
}

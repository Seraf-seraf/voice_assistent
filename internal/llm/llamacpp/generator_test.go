package llamacpp

import (
	"context"
	"errors"
	"math"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/dialogue"
	"github.com/Seraf-seraf/voice_assistent/internal/llm"
)

type fakeTokenBackend struct {
	mu           sync.Mutex
	template     string
	pieces       []tokenPiece
	nextErr      error
	beginErr     error
	endErr       error
	closeErr     error
	beginCount   int
	nextCount    int
	endCount     int
	closeCount   int
	beginPrompt  string
	beginOptions llm.Options
	beginContext context.Context
	blockNext    chan struct{}
	nextEntered  chan struct{}
}

func (b *fakeTokenBackend) Template() string { return b.template }
func (b *fakeTokenBackend) Begin(ctx context.Context, prompt string, options llm.Options) error {
	b.mu.Lock()
	b.beginCount++
	b.beginPrompt = prompt
	b.beginOptions = options
	b.beginContext = ctx
	b.mu.Unlock()
	return b.beginErr
}
func (b *fakeTokenBackend) Next(ctx context.Context) (tokenPiece, error) {
	b.mu.Lock()
	b.nextCount++
	index := b.nextCount - 1
	b.mu.Unlock()
	if b.nextEntered != nil && index == 0 {
		close(b.nextEntered)
	}
	if b.blockNext != nil && index == 0 {
		select {
		case <-b.blockNext:
		case <-ctx.Done():
			return tokenPiece{}, ctx.Err()
		}
	}
	if b.nextErr != nil && index >= len(b.pieces) {
		return tokenPiece{}, b.nextErr
	}
	if index >= len(b.pieces) {
		return tokenPiece{End: true}, nil
	}
	return b.pieces[index], nil
}
func (b *fakeTokenBackend) End() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.endCount++
	return b.endErr
}
func (b *fakeTokenBackend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closeCount++
	return b.closeErr
}

func validSnapshot() dialogue.Snapshot {
	return dialogue.Snapshot{
		SystemPrompt: "system", ResponsePolicy: "policy",
		Messages: []dialogue.Message{{Role: dialogue.RoleUser, Content: "hello"}},
	}
}

func validLLMOptions() llm.Options { return llm.Options{Temperature: 0.4, MaxTokens: 5} }

func TestValidateOptionsBoundaries(t *testing.T) {
	base := Options{ModelPath: "model.gguf", LibraryDir: "libs", ContextSize: 4096, GPULayers: 99, Threads: 4, Timeout: time.Second}
	cases := []struct {
		name string
		edit func(*Options)
		want bool
	}{
		{"context zero", func(o *Options) { o.ContextSize = 0 }, false},
		{"context negative", func(o *Options) { o.ContextSize = -1 }, false},
		{"context overflow", func(o *Options) { o.ContextSize = int(maxInt32) + 1 }, false},
		{"threads zero", func(o *Options) { o.Threads = 0 }, false},
		{"threads negative", func(o *Options) { o.Threads = -1 }, false},
		{"threads overflow", func(o *Options) { o.Threads = int(maxInt32) + 1 }, false},
		{"GPU negative", func(o *Options) { o.GPULayers = -1 }, false},
		{"GPU overflow", func(o *Options) { o.GPULayers = int(maxInt32) + 1 }, false},
		{"GPU zero", func(o *Options) { o.GPULayers = 0 }, true},
		{"timeout zero", func(o *Options) { o.Timeout = 0 }, false},
		{"timeout negative", func(o *Options) { o.Timeout = -time.Second }, false},
		{"empty model", func(o *Options) { o.ModelPath = "" }, false},
		{"NUL library", func(o *Options) { o.LibraryDir = "lib\x00dir" }, false},
		{"NUL model", func(o *Options) { o.ModelPath = "model\x00.gguf" }, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			options := base
			test.edit(&options)
			err := validateOptions(options)
			if got := err == nil; got != test.want {
				t.Fatalf("validateOptions() error=%v, want valid=%v", err, test.want)
			}
			if !test.want && !errors.Is(err, ErrInvalidOptions) {
				t.Fatalf("error %v does not wrap ErrInvalidOptions", err)
			}
		})
	}
}

const maxInt32 = int64(math.MaxInt32)

func TestOpenRejectsInputsBeforeNativeLoad(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Open(ctx, Options{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Open()=%v", err)
	}
	options := Options{ModelPath: "missing.gguf", LibraryDir: "missing", ContextSize: 8, Threads: 1, Timeout: time.Second}
	if _, err := Open(context.Background(), options); err == nil {
		t.Fatal("missing model accepted")
	}
	dir := t.TempDir()
	model := dir + "/bad.gguf"
	if err := os.WriteFile(model, []byte("nope"), 0600); err != nil {
		t.Fatal(err)
	}
	options.ModelPath, options.LibraryDir = model, dir
	if _, err := Open(context.Background(), options); err == nil || !strings.Contains(err.Error(), "сигнатуру GGUF") {
		t.Fatalf("invalid magic error=%v", err)
	}
}

func TestGenerateRendersRequestAndHonorsTokenBudget(t *testing.T) {
	backend := &fakeTokenBackend{template: "{% for message in messages %}{{ message.role }}={{ message.content }};{% endfor %}{% if enable_thinking %}thinking{% endif %}{% if add_generation_prompt %}assistant={% endif %}", pieces: []tokenPiece{{Bytes: []byte("hello")}, {End: true}}}
	generator, err := newGenerator(backend, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	err = generator.Generate(context.Background(), llm.Request{Dialogue: validSnapshot(), Options: validLLMOptions()}, func(delta llm.TextDelta) error {
		got = append(got, delta.Text)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"hello"}) {
		t.Fatalf("deltas=%q", got)
	}
	if strings.Count(backend.beginPrompt, "user=hello") != 1 || !strings.Contains(backend.beginPrompt, "system=system\n\npolicy") || strings.Contains(backend.beginPrompt, "thinking") {
		t.Fatalf("rendered prompt=%q", backend.beginPrompt)
	}
	if backend.beginOptions != validLLMOptions() || backend.nextCount != 2 || backend.endCount != 1 {
		t.Fatalf("options=%+v next=%d end=%d", backend.beginOptions, backend.nextCount, backend.endCount)
	}
}

func TestGenerateValidationErrorsDoNotBegin(t *testing.T) {
	backend := &fakeTokenBackend{template: "{{ messages[0].content }}"}
	generator, _ := newGenerator(backend, time.Second)
	if err := generator.Generate(context.Background(), llm.Request{Dialogue: validSnapshot(), Options: llm.Options{Temperature: math.NaN(), MaxTokens: 1}}, func(llm.TextDelta) error { return nil }); !errors.Is(err, llm.ErrInvalidOptions) {
		t.Fatalf("invalid options error=%v", err)
	}
	if err := generator.Generate(context.Background(), llm.Request{Dialogue: validSnapshot(), Options: validLLMOptions()}, nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("nil emit error=%v", err)
	}
	if backend.beginCount != 0 {
		t.Fatalf("Begin called %d times", backend.beginCount)
	}
}

func TestNewGeneratorRejectsNilBackendAndInvalidTimeout(t *testing.T) {
	var nilBackend *fakeTokenBackend
	if _, err := newGenerator(nilBackend, time.Second); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("typed nil backend error=%v", err)
	}
	if _, err := newGenerator(&fakeTokenBackend{}, 0); !errors.Is(err, ErrInvalidOptions) {
		t.Fatal("zero timeout accepted")
	}
}

func TestGenerateStopsOnEmitErrorAndEndsPartialBegin(t *testing.T) {
	sentinel := errors.New("emit failed")
	backend := &fakeTokenBackend{template: "{{ messages[0].content }}", pieces: []tokenPiece{{Bytes: []byte("one")}, {Bytes: []byte("two")}}}
	generator, _ := newGenerator(backend, time.Second)
	err := generator.Generate(context.Background(), llm.Request{Dialogue: validSnapshot(), Options: validLLMOptions()}, func(llm.TextDelta) error { return sentinel })
	if !errors.Is(err, sentinel) || backend.nextCount != 1 || backend.endCount != 1 {
		t.Fatalf("Generate()=%v next=%d end=%d", err, backend.nextCount, backend.endCount)
	}
	beginFailure := errors.New("partial Begin failure")
	backend = &fakeTokenBackend{template: "{{ messages[0].content }}", beginErr: beginFailure}
	generator, _ = newGenerator(backend, time.Second)
	err = generator.Generate(context.Background(), llm.Request{Dialogue: validSnapshot(), Options: validLLMOptions()}, func(llm.TextDelta) error { return nil })
	if !errors.Is(err, beginFailure) || backend.beginCount != 1 || backend.endCount != 1 {
		t.Fatalf("partial Begin Generate()=%v begin=%d end=%d", err, backend.beginCount, backend.endCount)
	}
}

func TestGenerateMaxTokensLimitsNextCalls(t *testing.T) {
	backend := &fakeTokenBackend{template: "{{ messages[0].content }}", pieces: []tokenPiece{{Bytes: []byte("x")}, {Bytes: []byte("y")}}}
	generator, _ := newGenerator(backend, time.Second)
	err := generator.Generate(context.Background(), llm.Request{Dialogue: validSnapshot(), Options: llm.Options{Temperature: 0, MaxTokens: 1}}, func(llm.TextDelta) error { return nil })
	if err != nil || backend.nextCount != 1 {
		t.Fatalf("Generate()=%v Next calls=%d", err, backend.nextCount)
	}
}

func TestGenerateErrorsPreserveCauseAndEndCleanup(t *testing.T) {
	generateErr, endErr := errors.New("native error"), errors.New("end error")
	backend := &fakeTokenBackend{template: "{{ messages[0].content }}", pieces: []tokenPiece{{Bytes: []byte("partial")}}, nextErr: generateErr, endErr: endErr}
	generator, _ := newGenerator(backend, time.Second)
	err := generator.Generate(context.Background(), llm.Request{Dialogue: validSnapshot(), Options: validLLMOptions()}, func(llm.TextDelta) error { return nil })
	if !errors.Is(err, generateErr) || !errors.Is(err, endErr) || backend.closeCount != 1 {
		t.Fatalf("Generate()=%v, close count=%d", err, backend.closeCount)
	}
	if err := generator.Generate(context.Background(), llm.Request{}, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("Generate after failed End=%v", err)
	}
}

func TestGenerateContextTimeoutReachesBackend(t *testing.T) {
	backend := &fakeTokenBackend{template: "{{ messages[0].content }}", blockNext: make(chan struct{}), nextEntered: make(chan struct{})}
	generator, _ := newGenerator(backend, 20*time.Millisecond)
	err := generator.Generate(context.Background(), llm.Request{Dialogue: validSnapshot(), Options: validLLMOptions()}, func(llm.TextDelta) error { return nil })
	if !errors.Is(err, context.DeadlineExceeded) || backend.beginContext.Err() == nil {
		t.Fatalf("Generate()=%v backend context=%v", err, backend.beginContext.Err())
	}
}

func TestGenerateBusyAndCloseLifecycle(t *testing.T) {
	backend := &fakeTokenBackend{template: "{{ messages[0].content }}", blockNext: make(chan struct{}), nextEntered: make(chan struct{})}
	generator, _ := newGenerator(backend, time.Second)
	done := make(chan error, 1)
	go func() {
		done <- generator.Generate(context.Background(), llm.Request{Dialogue: validSnapshot(), Options: validLLMOptions()}, func(llm.TextDelta) error { return nil })
	}()
	select {
	case <-backend.nextEntered:
	case <-time.After(time.Second):
		t.Fatal("Generate did not reach backend")
	}
	if err := generator.Generate(context.Background(), llm.Request{Dialogue: validSnapshot(), Options: validLLMOptions()}, func(llm.TextDelta) error { return nil }); !errors.Is(err, ErrBusy) {
		t.Fatalf("concurrent Generate()=%v", err)
	}
	if err := generator.Close(); !errors.Is(err, ErrBusy) || backend.closeCount != 0 {
		t.Fatalf("Close during Generate()=%v closeCount=%d", err, backend.closeCount)
	}
	close(backend.blockNext)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := generator.Close(); err != nil {
		t.Fatal(err)
	}
	if err := generator.Close(); err != nil || backend.closeCount != 1 {
		t.Fatalf("repeated Close()=%v count=%d", err, backend.closeCount)
	}
}

func TestGenerateCancellationStopsBeforeEmit(t *testing.T) {
	backend := &fakeTokenBackend{template: "{{ messages[0].content }}", pieces: []tokenPiece{{Bytes: []byte("x")}}}
	generator, _ := newGenerator(backend, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	var emits int
	err := generator.Generate(ctx, llm.Request{Dialogue: validSnapshot(), Options: validLLMOptions()}, func(llm.TextDelta) error {
		emits++
		cancel()
		return nil
	})
	if err == nil || emits != 1 || backend.endCount != 1 {
		t.Fatalf("Generate()=%v emits=%d end=%d", err, emits, backend.endCount)
	}
}

func TestGenerateObservesCancellationInsideFinalEmit(t *testing.T) {
	backend := &fakeTokenBackend{template: "{{ messages[0].content }}", pieces: []tokenPiece{{Bytes: []byte("<th")}, {End: true}}}
	generator, _ := newGenerator(backend, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	err := generator.Generate(ctx, llm.Request{Dialogue: validSnapshot(), Options: llm.Options{Temperature: 0, MaxTokens: 2}}, func(llm.TextDelta) error {
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Generate()=%v, want context.Canceled", err)
	}
}

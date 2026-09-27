package sherpa

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/audio"
	"github.com/Seraf-seraf/voice_assistent/internal/tts"
)

type fakeBackend struct {
	pcm         audio.PCM
	err         error
	closeCount  int
	onSynthesis func(context.Context)
	calls       int
}

func (b *fakeBackend) synthesize(ctx context.Context, _ string) (audio.PCM, error) {
	b.calls++
	if b.onSynthesis != nil {
		b.onSynthesis(ctx)
	}
	return b.pcm, b.err
}

func (b *fakeBackend) close() error { b.closeCount++; return nil }

func TestOptionsValidate(t *testing.T) {
	for _, test := range []struct {
		name    string
		options Options
		wantErr bool
	}{
		{name: "valid", options: Options{ModelDir: "/models/voice", Threads: 2}},
		{name: "empty path", options: Options{Threads: 2}, wantErr: true},
		{name: "whitespace path", options: Options{ModelDir: " \t", Threads: 2}, wantErr: true},
		{name: "nul path", options: Options{ModelDir: "/models/\x00voice", Threads: 2}, wantErr: true},
		{name: "zero threads", options: Options{ModelDir: "/models/voice"}, wantErr: true},
		{name: "threads below range", options: Options{ModelDir: "/models/voice", Threads: -1}, wantErr: true},
		{name: "threads upper bound", options: Options{ModelDir: "/models/voice", Threads: 32}},
		{name: "threads above range", options: Options{ModelDir: "/models/voice", Threads: 33}, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.options.Validate()
			if (err != nil) != test.wantErr {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestEngineSynthesizeValidatesAndWrapsBackend(t *testing.T) {
	backendErr := errors.New("native synthesis failed")
	backend := &fakeBackend{err: backendErr}
	engine := newEngine(backend)
	if _, err := engine.Synthesize(context.Background(), strings.Repeat("ё", 201)); !errors.Is(err, tts.ErrInvalidText) || backend.calls != 0 {
		t.Fatalf("invalid text error=%v backend calls=%d", err, backend.calls)
	}
	if _, err := engine.Synthesize(context.Background(), "тест"); !errors.Is(err, backendErr) || !errors.Is(err, ErrSynthesis) {
		t.Fatalf("Synthesize() error = %v, want wrapped backend error", err)
	}
}

func TestEngineSynthesizeValidatesNativeAudio(t *testing.T) {
	for _, test := range []struct {
		name    string
		pcm     audio.PCM
		wantErr error
	}{
		{name: "empty", pcm: audio.PCM{}, wantErr: ErrSynthesis},
		{name: "wrong rate", pcm: audio.PCM{Samples: []float32{0}, SampleRate: 16000}, wantErr: ErrInvalidAudio},
		{name: "invalid sample", pcm: audio.PCM{Samples: []float32{float32(math.Inf(1))}, SampleRate: 22050}, wantErr: ErrInvalidAudio},
		{name: "duration too long", pcm: audio.PCM{Samples: make([]float32, 22050*61), SampleRate: 22050}, wantErr: ErrInvalidAudio},
		{name: "valid", pcm: audio.PCM{Samples: []float32{0, 0.5, -1, 1}, SampleRate: 22050}},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := &fakeBackend{pcm: test.pcm}
			_, err := newEngine(backend).Synthesize(context.Background(), "проверка")
			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Fatalf("Synthesize() error = %v, want %v", err, test.wantErr)
			}
			if test.wantErr == nil && err != nil {
				t.Fatalf("Synthesize() error = %v", err)
			}
		})
	}
}

func TestEngineSynthesizeCancellationBeforeAndAfterBackend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	backend := &fakeBackend{pcm: audio.PCM{Samples: []float32{0.5}, SampleRate: 22050}}
	if _, err := newEngine(backend).Synthesize(ctx, "проверка"); !errors.Is(err, context.Canceled) || backend.calls != 0 {
		t.Fatalf("pre-canceled Synthesize() error=%v calls=%d", err, backend.calls)
	}
	postCallCtx, postCallCancel := context.WithCancel(context.Background())
	defer postCallCancel()
	backend = &fakeBackend{
		pcm:         audio.PCM{Samples: []float32{0.5}, SampleRate: 22050},
		onSynthesis: func(context.Context) { postCallCancel() },
	}
	if _, err := newEngine(backend).Synthesize(postCallCtx, "проверка"); !errors.Is(err, context.Canceled) {
		t.Fatalf("post-backend Synthesize() error = %v, want cancellation", err)
	}
	backendCtx, backendCancel := context.WithCancel(context.Background())
	defer backendCancel()
	backendErr := errors.New("backend error while canceled")
	backend = &fakeBackend{
		err:         backendErr,
		onSynthesis: func(context.Context) { backendCancel() },
	}
	if _, err := newEngine(backend).Synthesize(backendCtx, "проверка"); !errors.Is(err, context.Canceled) || !errors.Is(err, backendErr) {
		t.Fatalf("canceled backend failure = %v, want cancellation and backend cause", err)
	}
}

func TestEngineCloseIsIdempotent(t *testing.T) {
	backend := &fakeBackend{pcm: audio.PCM{Samples: []float32{0.5}, SampleRate: 22050}}
	engine := newEngine(backend)
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	if err := engine.Close(); err != nil || backend.closeCount != 1 {
		t.Fatalf("second Close() error=%v close count=%d", err, backend.closeCount)
	}
	if _, err := engine.Synthesize(context.Background(), "проверка"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Synthesize() after Close() error = %v", err)
	}
}

func TestOpenRejectsMissingPinnedAssetsBeforeNativeOpen(t *testing.T) {
	err := preflightAssets(Options{ModelDir: t.TempDir(), Threads: 2})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("preflightAssets() error = %v, want ErrUnavailable", err)
	}
}

func TestMaximumPhraseAudioDurationIsOneMinute(t *testing.T) {
	if maxPhraseAudioDuration != time.Minute {
		t.Fatalf("maxPhraseAudioDuration = %s", maxPhraseAudioDuration)
	}
}

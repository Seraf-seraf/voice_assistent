package pulse

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Seraf-seraf/voice_assistent/internal/service/audio"
)

type fakeBackend struct {
	events   []string
	written  []float32
	drainErr error
	flushErr error
	closeErr error
	drain    func(context.Context) error
}

func (b *fakeBackend) Write(_ context.Context, samples []float32) error {
	b.events = append(b.events, "write")
	b.written = append(b.written, samples...)
	return nil
}

func (b *fakeBackend) Drain(ctx context.Context) error {
	b.events = append(b.events, "drain")
	if b.drain != nil {
		return b.drain(ctx)
	}
	return b.drainErr
}

func (b *fakeBackend) Flush() error {
	b.events = append(b.events, "flush")
	return b.flushErr
}

func (b *fakeBackend) Close() error {
	b.events = append(b.events, "close")
	return b.closeErr
}

func TestPlayerWritesInOrderAndWaitsForDrain(t *testing.T) {
	backend := &fakeBackend{}
	player := newPlayer(backend, 22050)
	pcm := audio.PCM{Samples: []float32{0.25, -0.5, 0.75}, SampleRate: 22050}

	if err := player.Play(context.Background(), pcm); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(backend.written, pcm.Samples) {
		t.Fatalf("written samples = %v, want %v", backend.written, pcm.Samples)
	}
	if !reflect.DeepEqual(backend.events, []string{"write", "drain"}) {
		t.Fatalf("events = %v", backend.events)
	}
}

func TestPlayerFlushesAndPreservesDrainAndCleanupErrors(t *testing.T) {
	drainErr := errors.New("drain failed")
	flushErr := errors.New("flush failed")
	backend := &fakeBackend{drainErr: drainErr, flushErr: flushErr}
	player := newPlayer(backend, 22050)

	err := player.Play(context.Background(), audio.PCM{Samples: []float32{0.5}, SampleRate: 22050})
	if !errors.Is(err, drainErr) || !errors.Is(err, flushErr) {
		t.Fatalf("Play error = %v", err)
	}
	if !reflect.DeepEqual(backend.events, []string{"write", "drain", "flush"}) {
		t.Fatalf("events = %v", backend.events)
	}
}

func TestPlayerCancellationFlushesPendingAudio(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	backend := &fakeBackend{drain: func(ctx context.Context) error {
		cancel()
		return ctx.Err()
	}}
	player := newPlayer(backend, 22050)

	err := player.Play(ctx, audio.PCM{Samples: []float32{0.5}, SampleRate: 22050})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Play error = %v, want context cancellation", err)
	}
	if !reflect.DeepEqual(backend.events, []string{"write", "drain", "flush"}) {
		t.Fatalf("events = %v", backend.events)
	}
}

func TestPlayerRejectsRateBeforeNativeCalls(t *testing.T) {
	backend := &fakeBackend{}
	player := newPlayer(backend, 22050)

	err := player.Play(context.Background(), audio.PCM{Samples: []float32{0.5}, SampleRate: 16000})
	if err == nil {
		t.Fatal("Play succeeded with a mismatched sample rate")
	}
	if len(backend.events) != 0 {
		t.Fatalf("native calls = %v", backend.events)
	}
}

func TestPlayerCloseIsIdempotent(t *testing.T) {
	backend := &fakeBackend{}
	player := newPlayer(backend, 22050)

	if err := player.Close(); err != nil {
		t.Fatal(err)
	}
	if err := player.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(backend.events, []string{"close"}) {
		t.Fatalf("events = %v", backend.events)
	}
}

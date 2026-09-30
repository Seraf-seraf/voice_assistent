//go:build playback_integration && linux && amd64 && cgo && !android && !musl

package pulse

import (
	"context"
	"errors"
	"math"
	"os"
	"testing"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/service/audio"
)

func TestPulsePlaybackOnSelectedSink(t *testing.T) {
	device := os.Getenv("ASSISTANT_PLAYBACK_TEST_DEVICE")
	if device == "" {
		t.Fatal("ASSISTANT_PLAYBACK_TEST_DEVICE обязателен для playback_integration")
	}
	player, err := Open(context.Background(), Options{Device: device, SampleRate: 22050})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := player.Close(); err != nil {
			t.Errorf("закрыть аудиовыход: %v", err)
		}
	})

	short := testTone(22050 / 8)
	for range 2 {
		if err := player.Play(context.Background(), short); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	finished := make(chan error, 1)
	go func() {
		finished <- player.Play(ctx, testTone(22050*2))
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("воспроизведение после отмены вернуло %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("воспроизведение не остановилось после отмены")
	}
}

func testTone(frames int) audio.PCM {
	samples := make([]float32, frames)
	for index := range samples {
		samples[index] = float32(math.Sin(2*math.Pi*440*float64(index)/22050) * 0.02)
	}
	return audio.PCM{Samples: samples, SampleRate: 22050}
}

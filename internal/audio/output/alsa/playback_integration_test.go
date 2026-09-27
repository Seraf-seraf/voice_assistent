//go:build playback_integration && linux && amd64 && cgo && !android && !musl

package alsa

import (
	"context"
	"errors"
	"math"
	"os"
	"testing"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/audio"
)

func TestSelectedALSADevicePlayback(t *testing.T) {
	deviceName := os.Getenv("ASSISTANT_PLAYBACK_TEST_DEVICE")
	if deviceName == "" {
		t.Fatal("ASSISTANT_PLAYBACK_TEST_DEVICE обязателен для playback_integration")
	}
	player, err := Open(context.Background(), Options{Device: deviceName, SampleRate: 22050})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := player.Close(); err != nil {
			t.Errorf("закрыть ALSA player: %v", err)
		}
	}()
	pcm := lowLevelSignal(22050 / 10)
	for range 2 {
		if err := player.Play(context.Background(), pcm); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	playDone := make(chan error, 1)
	started := make(chan struct{})
	longPCM := lowLevelSignal(22050 * 5)
	go func() {
		close(started)
		playDone <- player.Play(ctx, longPCM)
	}()
	<-started
	cancel()
	select {
	case err := <-playDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("отменённый Play вернул %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Play не завершился после отмены")
	}
}

func lowLevelSignal(samples int) audio.PCM {
	values := make([]float32, samples)
	for i := range values {
		values[i] = float32(math.Sin(2*math.Pi*440*float64(i)/22050) * 0.02)
	}
	return audio.PCM{Samples: values, SampleRate: 22050}
}

//go:build playback_integration && linux && amd64 && cgo && !android && !musl

package alsa

import (
	"context"
	"errors"
	"math"
	"os"
	"sync"
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
	defer cancel()
	writeGate := &playbackWriteGate{pcmDevice: player.device, written: make(chan struct{}), release: make(chan struct{})}
	player.device = writeGate
	playDone := make(chan error, 1)
	longPCM := lowLevelSignal(22050 * 5)
	go func() { playDone <- player.Play(ctx, longPCM) }()
	doneObserved := false
	var releaseOnce sync.Once
	defer func() {
		releaseOnce.Do(func() { close(writeGate.release) })
		if !doneObserved {
			<-playDone
		}
	}()
	select {
	case <-writeGate.written:
	case err := <-playDone:
		doneObserved = true
		t.Fatalf("Play завершился до первой записи PCM: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("нативная запись ALSA не достигла тестового барьера")
	}
	cancel()
	releaseOnce.Do(func() { close(writeGate.release) })
	select {
	case err := <-playDone:
		doneObserved = true
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("отменённый Play вернул %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Play не завершился после отмены")
	}
	if writeGate.dropCount != 1 {
		t.Fatalf("Drop вызван %d раз, ожидался один", writeGate.dropCount)
	}
	if err := player.Play(context.Background(), pcm); err != nil {
		t.Fatalf("повторный Play на том же устройстве: %v", err)
	}
}

type playbackWriteGate struct {
	pcmDevice
	written   chan struct{}
	release   chan struct{}
	once      sync.Once
	dropCount int
}

func (d *playbackWriteGate) Write(samples []float32) (int, error) {
	written, err := d.pcmDevice.Write(samples)
	if written > 0 {
		d.once.Do(func() {
			close(d.written)
			<-d.release
		})
	}
	return written, err
}

func (d *playbackWriteGate) Drop() error {
	d.dropCount++
	return d.pcmDevice.Drop()
}

func lowLevelSignal(samples int) audio.PCM {
	values := make([]float32, samples)
	for i := range values {
		values[i] = float32(math.Sin(2*math.Pi*440*float64(i)/22050) * 0.02)
	}
	return audio.PCM{Samples: values, SampleRate: 22050}
}

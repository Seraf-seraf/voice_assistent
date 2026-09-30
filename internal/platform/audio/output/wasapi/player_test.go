package wasapi

import (
	"context"
	"errors"
	"testing"

	"github.com/Seraf-seraf/voice_assistent/internal/service/audio"
)

type fakeDevice struct {
	playErr  error
	closeErr error
	played   int
	closed   int
}

func (d *fakeDevice) Play(context.Context, []float32) error {
	d.played++
	return d.playErr
}

func (d *fakeDevice) Close() error {
	d.closed++
	return d.closeErr
}

func TestPlayerDelegatesAndClosesOnce(t *testing.T) {
	device := &fakeDevice{}
	player := newPlayer(device, 22050)
	pcm := audio.PCM{Samples: []float32{0.25, -0.5}, SampleRate: 22050}

	if err := player.Play(context.Background(), pcm); err != nil {
		t.Fatal(err)
	}
	if err := player.Close(); err != nil {
		t.Fatal(err)
	}
	if err := player.Close(); err != nil {
		t.Fatal(err)
	}
	if device.played != 1 || device.closed != 1 {
		t.Fatalf("device calls: played=%d closed=%d", device.played, device.closed)
	}
}

func TestPlayerPreservesDeviceFailure(t *testing.T) {
	playErr := errors.New("WASAPI failed")
	device := &fakeDevice{playErr: playErr}
	player := newPlayer(device, 22050)

	err := player.Play(context.Background(), audio.PCM{Samples: []float32{0.5}, SampleRate: 22050})
	if !errors.Is(err, playErr) {
		t.Fatalf("Play error = %v", err)
	}
}

func TestPlayerRejectsMismatchedRateBeforeDeviceCall(t *testing.T) {
	device := &fakeDevice{}
	player := newPlayer(device, 22050)

	err := player.Play(context.Background(), audio.PCM{Samples: []float32{0.5}, SampleRate: 16000})
	if err == nil || device.played != 0 {
		t.Fatalf("Play error=%v, device calls=%d", err, device.played)
	}
}

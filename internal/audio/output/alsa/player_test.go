package alsa

import (
	"context"
	"errors"
	"reflect"
	"syscall"
	"testing"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/audio"
)

type fakePCMDevice struct {
	writes         []int
	writeErrors    []error
	accepted       []float32
	waitErr        error
	waitEntered    chan struct{}
	waitImmediate  bool
	drainErrors    []error
	drainEntered   chan struct{}
	drainAlwaysErr error
	prepareErr     error
	dropErr        error
	closeCount     int
	prepareCount   int
	dropCount      int
	drainCount     int
}

func (d *fakePCMDevice) Write(samples []float32) (int, error) {
	n := len(samples)
	if len(d.writes) > 0 {
		n = d.writes[0]
		d.writes = d.writes[1:]
	}
	if n > len(samples) {
		return n, nil
	}
	if n > 0 {
		d.accepted = append(d.accepted, samples[:n]...)
	}
	var err error
	if len(d.writeErrors) > 0 {
		err = d.writeErrors[0]
		d.writeErrors = d.writeErrors[1:]
	}
	return n, err
}

func (d *fakePCMDevice) Wait(ctx context.Context, _ time.Duration) error {
	if d.waitEntered != nil {
		select {
		case d.waitEntered <- struct{}{}:
		default:
		}
	}
	if d.waitErr != nil {
		return d.waitErr
	}
	if d.waitImmediate {
		return nil
	}
	<-ctx.Done()
	return ctx.Err()
}

func (d *fakePCMDevice) Drain() error {
	d.drainCount++
	if d.drainEntered != nil {
		select {
		case d.drainEntered <- struct{}{}:
		default:
		}
	}
	if d.drainAlwaysErr != nil {
		return d.drainAlwaysErr
	}
	if len(d.drainErrors) == 0 {
		return nil
	}
	err := d.drainErrors[0]
	d.drainErrors = d.drainErrors[1:]
	return err
}

func (d *fakePCMDevice) Drop() error    { d.dropCount++; return d.dropErr }
func (d *fakePCMDevice) Prepare() error { d.prepareCount++; return d.prepareErr }
func (d *fakePCMDevice) Close() error   { d.closeCount++; return nil }

func TestPlayerPlayRetriesShortWritesWithoutDuplicatingSamples(t *testing.T) {
	device := &fakePCMDevice{
		writes: []int{2, 0, 1, 2}, writeErrors: []error{nil, syscall.EAGAIN, nil, nil}, waitImmediate: true,
	}
	player := newPlayer(device, 22050)
	input := []float32{0.1, 0.2, 0.3, 0.4, 0.5}
	if err := player.Play(context.Background(), audio.PCM{Samples: input, SampleRate: 22050}); err != nil {
		t.Fatalf("Play() error = %v", err)
	}
	if !reflect.DeepEqual(device.accepted, input) || device.dropCount != 0 || device.drainCount != 1 {
		t.Fatalf("accepted=%v drops=%d drains=%d", device.accepted, device.dropCount, device.drainCount)
	}
}

func TestPlayerCancellationWhileWaitingDropsPendingFrames(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	device := &fakePCMDevice{writes: []int{0}, writeErrors: []error{syscall.EAGAIN}, waitEntered: make(chan struct{}, 1)}
	player := newPlayer(device, 22050)
	go func() {
		<-device.waitEntered
		cancel()
	}()
	err := player.Play(ctx, audio.PCM{Samples: []float32{0.1}, SampleRate: 22050})
	if !errors.Is(err, context.Canceled) || device.dropCount != 1 {
		t.Fatalf("Play() error=%v drop count=%d", err, device.dropCount)
	}
}

func TestPlayerCancellationDuringDrainDropsPendingFrames(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	device := &fakePCMDevice{drainEntered: make(chan struct{}, 1), drainAlwaysErr: syscall.EAGAIN}
	player := newPlayer(device, 22050)
	playDone := make(chan error, 1)
	go func() {
		playDone <- player.Play(ctx, audio.PCM{Samples: []float32{0.2}, SampleRate: 22050})
	}()
	<-device.drainEntered
	cancel()
	if err := <-playDone; !errors.Is(err, context.Canceled) || device.dropCount != 1 {
		t.Fatalf("Play() error=%v drop count=%d", err, device.dropCount)
	}
}

func TestPlayerPlayPreservesSequenceAcrossShortWrites(t *testing.T) {
	input := []float32{0.1, 0.2, 0.3, 0.4, 0.5}
	device := &fakePCMDevice{writes: []int{2, 1, 2}}
	player := newPlayer(device, 22050)
	if err := player.Play(context.Background(), audio.PCM{Samples: input, SampleRate: 22050}); err != nil {
		t.Fatalf("Play() error = %v", err)
	}
	if !reflect.DeepEqual(device.accepted, input) || device.prepareCount != 1 || device.drainCount != 1 || device.dropCount != 0 {
		t.Fatalf("accepted=%v prepare=%d drain=%d drop=%d", device.accepted, device.prepareCount, device.drainCount, device.dropCount)
	}
}

func TestPlayerPlayDropsAfterWriteAndKeepsCleanupError(t *testing.T) {
	writeErr := errors.New("write failed")
	dropErr := errors.New("drop failed")
	device := &fakePCMDevice{writeErrors: []error{writeErr}, dropErr: dropErr}
	player := newPlayer(device, 22050)
	err := player.Play(context.Background(), audio.PCM{Samples: []float32{0.5}, SampleRate: 22050})
	if !errors.Is(err, writeErr) || !errors.Is(err, dropErr) {
		t.Fatalf("Play() error = %v, want write and drop failures", err)
	}
}

func TestPlayerPlayValidatesRateBeforeWriting(t *testing.T) {
	device := &fakePCMDevice{}
	player := newPlayer(device, 22050)
	err := player.Play(context.Background(), audio.PCM{Samples: []float32{0.5}, SampleRate: 16000})
	if err == nil || device.prepareCount != 0 || len(device.accepted) != 0 {
		t.Fatalf("Play() error=%v prepare=%d writes=%d", err, device.prepareCount, len(device.accepted))
	}
}

func TestPlayerCloseIsIdempotent(t *testing.T) {
	device := &fakePCMDevice{}
	player := newPlayer(device, 22050)
	if err := player.Close(); err != nil {
		t.Fatal(err)
	}
	if err := player.Close(); err != nil || device.closeCount != 1 {
		t.Fatalf("second Close() error=%v close count=%d", err, device.closeCount)
	}
}

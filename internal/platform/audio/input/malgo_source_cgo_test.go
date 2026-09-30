//go:build cgo

package input

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMalgoSourceFramesInputBlock(t *testing.T) {
	format := testFormat()
	sourceInterface, err := NewMalgoSource(MalgoOptions{Format: format, QueueSize: 1})
	if err != nil {
		t.Fatalf("NewMalgoSource() error: %v", err)
	}
	source := sourceInterface.(*malgoSource)
	t.Cleanup(func() {
		if err := source.Close(); err != nil {
			t.Errorf("Close() error: %v", err)
		}
	})

	capturedAt := time.Unix(100, 0)
	if err := source.writeInput(pcm16(9, format.SamplesPerFrame()), uint32(format.SamplesPerFrame()), capturedAt); err != nil {
		t.Fatalf("writeInput() error: %v", err)
	}
	frame := receiveFrame(t, source.Frames())
	if want := capturedAt.Add(-format.FrameDuration); !frame.CapturedAt.Equal(want) {
		t.Fatalf("CapturedAt = %v, want %v", frame.CapturedAt, want)
	}
}

func TestMalgoSourceCloseBeforeRun(t *testing.T) {
	source, err := NewMalgoSource(MalgoOptions{Format: testFormat(), QueueSize: 1})
	if err != nil {
		t.Fatalf("NewMalgoSource() error: %v", err)
	}
	if err := source.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	if err := source.Run(context.Background()); !errors.Is(err, ErrSourceClosed) {
		t.Fatalf("Run() error = %v, want ErrSourceClosed", err)
	}
}

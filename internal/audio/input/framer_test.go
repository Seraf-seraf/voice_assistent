package input

import (
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/audio"
)

func TestFramerCombinesPartialBlocks(t *testing.T) {
	format := testFormat()
	framer := newTestFramer(t, format, 2)
	start := time.Unix(100, 0)

	first := pcm16(1, format.SamplesPerFrame()/2)
	second := pcm16(2, format.SamplesPerFrame()/2)
	if err := framer.WritePCM(first, start); err != nil {
		t.Fatalf("WritePCM(first) error: %v", err)
	}
	assertNoFrame(t, framer.Frames())
	if err := framer.WritePCM(second, start.Add(10*time.Millisecond)); err != nil {
		t.Fatalf("WritePCM(second) error: %v", err)
	}

	frame := receiveFrame(t, framer.Frames())
	if !frame.CapturedAt.Equal(start) {
		t.Fatalf("CapturedAt = %v, want %v", frame.CapturedAt, start)
	}
	if frame.Samples[0] != 1 || frame.Samples[len(frame.Samples)-1] != 2 {
		t.Fatalf("frame boundaries = %d..%d, want 1..2", frame.Samples[0], frame.Samples[len(frame.Samples)-1])
	}
}

func TestFramerPublishesMultipleFramesWithTimestamps(t *testing.T) {
	format := testFormat()
	framer := newTestFramer(t, format, 2)
	start := time.Unix(100, 0)

	if err := framer.WritePCM(pcm16(7, format.SamplesPerFrame()*2), start); err != nil {
		t.Fatalf("WritePCM() error: %v", err)
	}
	first := receiveFrame(t, framer.Frames())
	second := receiveFrame(t, framer.Frames())
	if !first.CapturedAt.Equal(start) || !second.CapturedAt.Equal(start.Add(format.FrameDuration)) {
		t.Fatalf("timestamps = %v, %v", first.CapturedAt, second.CapturedAt)
	}
}

func TestFramerDropsNewestFrameWhenQueueIsFull(t *testing.T) {
	format := testFormat()
	framer := newTestFramer(t, format, 1)
	start := time.Unix(100, 0)

	if err := framer.WritePCM(pcm16(5, format.SamplesPerFrame()*2), start); err != nil {
		t.Fatalf("WritePCM() error: %v", err)
	}
	if got := framer.DroppedFrames(); got != 1 {
		t.Fatalf("DroppedFrames() = %d, want 1", got)
	}
	if frame := receiveFrame(t, framer.Frames()); frame.Samples[0] != 5 {
		t.Fatalf("queued frame sample = %d, want 5", frame.Samples[0])
	}
}

func TestFramerResetDiscardsPartialBlock(t *testing.T) {
	format := testFormat()
	framer := newTestFramer(t, format, 1)
	start := time.Unix(100, 0)
	halfFrame := format.SamplesPerFrame() / 2

	if err := framer.WritePCM(pcm16(1, halfFrame), start); err != nil {
		t.Fatalf("WritePCM() error: %v", err)
	}
	if err := framer.Reset(); err != nil {
		t.Fatalf("Reset() error: %v", err)
	}
	if err := framer.WritePCM(pcm16(2, format.SamplesPerFrame()), start.Add(time.Second)); err != nil {
		t.Fatalf("WritePCM() after Reset error: %v", err)
	}

	frame := receiveFrame(t, framer.Frames())
	if frame.Samples[0] != 2 || !frame.CapturedAt.Equal(start.Add(time.Second)) {
		t.Fatalf("unexpected frame after reset: %+v", frame)
	}
}

func TestFramerValidatesInputAndLifecycle(t *testing.T) {
	format := testFormat()
	framer := newTestFramer(t, format, 1)
	if err := framer.WritePCM([]byte{1}, time.Now()); err == nil {
		t.Fatal("WritePCM() accepted odd byte count")
	}
	if err := framer.WritePCM([]byte{0, 0}, time.Time{}); err == nil {
		t.Fatal("WritePCM() accepted empty timestamp")
	}
	if err := framer.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	if err := framer.Close(); err != nil {
		t.Fatalf("second Close() error: %v", err)
	}
	if err := framer.WritePCM([]byte{0, 0}, time.Now()); !errors.Is(err, ErrClosed) {
		t.Fatalf("WritePCM() error = %v, want ErrClosed", err)
	}
	if _, open := <-framer.Frames(); open {
		t.Fatal("Frames() channel remains open")
	}
}

func TestNewFramerRejectsInvalidConfiguration(t *testing.T) {
	if _, err := NewFramer(audio.Format{}, 1); err == nil {
		t.Fatal("NewFramer() accepted empty format")
	}
	if _, err := NewFramer(testFormat(), 0); err == nil {
		t.Fatal("NewFramer() accepted zero queue size")
	}
}

func newTestFramer(t *testing.T, format audio.Format, queueSize int) *Framer {
	t.Helper()
	framer, err := NewFramer(format, queueSize)
	if err != nil {
		t.Fatalf("NewFramer() error: %v", err)
	}
	t.Cleanup(func() {
		if err := framer.Close(); err != nil {
			t.Errorf("Close() error: %v", err)
		}
	})
	return framer
}

func testFormat() audio.Format {
	return audio.Format{SampleRate: 16000, Channels: 1, FrameDuration: 20 * time.Millisecond}
}

func pcm16(value int16, count int) []byte {
	data := make([]byte, count*2)
	for index := 0; index < count; index++ {
		binary.LittleEndian.PutUint16(data[index*2:], uint16(value))
	}
	return data
}

func receiveFrame(t *testing.T, frames <-chan audio.Frame) audio.Frame {
	t.Helper()
	select {
	case frame := <-frames:
		return frame
	case <-time.After(time.Second):
		t.Fatal("frame not received")
		return audio.Frame{}
	}
}

func assertNoFrame(t *testing.T, frames <-chan audio.Frame) {
	t.Helper()
	select {
	case frame := <-frames:
		t.Fatalf("unexpected frame: %+v", frame)
	default:
	}
}

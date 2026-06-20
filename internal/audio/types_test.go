package audio

import (
	"strings"
	"testing"
	"time"
)

func TestFormatSamplesPerFrame(t *testing.T) {
	format := Format{SampleRate: 16000, Channels: 1, FrameDuration: 20 * time.Millisecond}
	if err := format.Validate(); err != nil {
		t.Fatalf("Validate() error: %v", err)
	}
	if got := format.SamplesPerFrame(); got != 320 {
		t.Fatalf("SamplesPerFrame() = %d, want 320", got)
	}
}

func TestFrameValidate(t *testing.T) {
	format := Format{SampleRate: 16000, Channels: 1, FrameDuration: 20 * time.Millisecond}
	frame := Frame{Samples: make([]int16, 319), CapturedAt: time.Now()}
	if err := frame.Validate(format); err == nil || !strings.Contains(err.Error(), "требуется 320") {
		t.Fatalf("Validate() error = %v, want sample count error", err)
	}
}

func TestUtteranceDuration(t *testing.T) {
	utterance := Utterance{
		Samples: make([]int16, 16000),
		Format:  Format{SampleRate: 16000, Channels: 1},
	}
	if got := utterance.Duration(); got != time.Second {
		t.Fatalf("Duration() = %v, want 1s", got)
	}
}

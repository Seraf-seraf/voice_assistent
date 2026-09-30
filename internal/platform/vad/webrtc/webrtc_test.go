package webrtc

import (
	"strings"
	"testing"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/service/audio"
	servicevad "github.com/Seraf-seraf/voice_assistent/internal/service/vad"
)

func TestWebRTCDetectorClassifiesSilence(t *testing.T) {
	format := audio.Format{SampleRate: 16000, Channels: 1, FrameDuration: 20 * time.Millisecond}
	detector, err := NewWebRTCDetector(format, 2)
	if err != nil {
		t.Fatalf("NewWebRTCDetector() error: %v", err)
	}
	t.Cleanup(func() {
		if err := detector.Close(); err != nil {
			t.Errorf("Close() error: %v", err)
		}
	})

	activity, err := detector.Classify(audio.Frame{
		Samples:    make([]int16, format.SamplesPerFrame()),
		CapturedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("Classify() error: %v", err)
	}
	if activity != servicevad.Silence {
		t.Fatalf("Classify() = %v, want Silence", activity)
	}
}

func TestWebRTCDetectorRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name           string
		format         audio.Format
		aggressiveness int
	}{
		{name: "sample rate", format: audio.Format{SampleRate: 44100, Channels: 1, FrameDuration: 20 * time.Millisecond}, aggressiveness: 2},
		{name: "channels", format: audio.Format{SampleRate: 16000, Channels: 2, FrameDuration: 20 * time.Millisecond}, aggressiveness: 2},
		{name: "frame duration", format: audio.Format{SampleRate: 16000, Channels: 1, FrameDuration: 25 * time.Millisecond}, aggressiveness: 2},
		{name: "aggressiveness", format: webRTCFormat(), aggressiveness: 4},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewWebRTCDetector(test.format, test.aggressiveness); err == nil {
				t.Fatal("NewWebRTCDetector() succeeded, want error")
			}
		})
	}
}

func TestWebRTCDetectorRejectsMalformedFrame(t *testing.T) {
	detector, err := NewWebRTCDetector(webRTCFormat(), 2)
	if err != nil {
		t.Fatalf("NewWebRTCDetector() error: %v", err)
	}
	t.Cleanup(func() { _ = detector.Close() })

	_, err = detector.Classify(audio.Frame{Samples: []int16{0}, CapturedAt: time.Now()})
	if err == nil || !strings.Contains(err.Error(), "требуется 320") {
		t.Fatalf("Classify() error = %v, want frame size error", err)
	}
}

func TestWebRTCDetectorCannotBeUsedAfterClose(t *testing.T) {
	detector, err := NewWebRTCDetector(webRTCFormat(), 2)
	if err != nil {
		t.Fatalf("NewWebRTCDetector() error: %v", err)
	}
	if err := detector.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	_, err = detector.Classify(audio.Frame{
		Samples:    make([]int16, 320),
		CapturedAt: time.Now(),
	})
	if err == nil || !strings.Contains(err.Error(), "закрыт") {
		t.Fatalf("Classify() error = %v, want closed error", err)
	}
}

func webRTCFormat() audio.Format {
	return audio.Format{SampleRate: 16000, Channels: 1, FrameDuration: 20 * time.Millisecond}
}

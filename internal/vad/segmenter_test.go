package vad

import (
	"testing"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/audio"
)

const frameDuration = 20 * time.Millisecond

func TestSegmenterBuildsUtteranceWithPreRollAndTrailingSilence(t *testing.T) {
	segmenter := newTestSegmenter(t, Settings{
		Format: testFormat(), PreRoll: 40 * time.Millisecond, MinSpeech: 40 * time.Millisecond,
		EndSilence: 40 * time.Millisecond, MaxUtterance: time.Second,
	})
	start := time.Unix(100, 0)

	processNoEvents(t, segmenter, testFrame(start, 1), Silence)
	processNoEvents(t, segmenter, testFrame(start.Add(frameDuration), 2), Silence)
	processNoEvents(t, segmenter, testFrame(start.Add(2*frameDuration), 3), Speech)
	events := process(t, segmenter, testFrame(start.Add(3*frameDuration), 4), Speech)
	started := requireEvent[SpeechStarted](t, events)
	if !started.At.Equal(start.Add(2 * frameDuration)) {
		t.Fatalf("SpeechStarted.At = %v", started.At)
	}

	processNoEvents(t, segmenter, testFrame(start.Add(4*frameDuration), 5), Silence)
	events = process(t, segmenter, testFrame(start.Add(5*frameDuration), 6), Silence)
	ended := requireEvent[SpeechEnded](t, events)
	if ended.Utterance.ID != 1 {
		t.Fatalf("Utterance.ID = %d, want 1", ended.Utterance.ID)
	}
	if len(ended.Utterance.Samples) != 6*testFormat().SamplesPerFrame() {
		t.Fatalf("samples = %d, want six frames", len(ended.Utterance.Samples))
	}
	if got := ended.Utterance.Samples[0]; got != 1 {
		t.Fatalf("first sample = %d, want pre-roll value 1", got)
	}
	wantEnd := start.Add(4 * frameDuration)
	if !ended.Utterance.EndedAt.Equal(wantEnd) {
		t.Fatalf("EndedAt = %v, want %v", ended.Utterance.EndedAt, wantEnd)
	}
}

func TestSegmenterDiscardsShortSpeech(t *testing.T) {
	segmenter := newTestSegmenter(t, Settings{
		Format: testFormat(), PreRoll: 40 * time.Millisecond, MinSpeech: 60 * time.Millisecond,
		EndSilence: 40 * time.Millisecond, MaxUtterance: time.Second,
	})
	start := time.Unix(100, 0)

	processNoEvents(t, segmenter, testFrame(start, 1), Speech)
	processNoEvents(t, segmenter, testFrame(start.Add(frameDuration), 2), Speech)
	processNoEvents(t, segmenter, testFrame(start.Add(2*frameDuration), 0), Silence)
	if events := segmenter.Flush(); len(events) != 0 {
		t.Fatalf("Flush() events = %v, want none", events)
	}
}

func TestSegmenterStopsAtMaximumDuration(t *testing.T) {
	segmenter := newTestSegmenter(t, Settings{
		Format: testFormat(), MinSpeech: frameDuration, EndSilence: 40 * time.Millisecond,
		MaxUtterance: 60 * time.Millisecond,
	})
	start := time.Unix(100, 0)

	requireEvent[SpeechStarted](t, process(t, segmenter, testFrame(start, 1), Speech))
	processNoEvents(t, segmenter, testFrame(start.Add(frameDuration), 2), Speech)
	ended := requireEvent[SpeechEnded](t, process(t, segmenter, testFrame(start.Add(2*frameDuration), 3), Speech))
	if ended.Utterance.Duration() != 60*time.Millisecond {
		t.Fatalf("Duration() = %v, want 60ms", ended.Utterance.Duration())
	}
}

func TestSegmenterFlushesActiveRecording(t *testing.T) {
	segmenter := newTestSegmenter(t, Settings{
		Format: testFormat(), MinSpeech: frameDuration, EndSilence: 40 * time.Millisecond,
		MaxUtterance: time.Second,
	})
	start := time.Unix(100, 0)

	requireEvent[SpeechStarted](t, process(t, segmenter, testFrame(start, 1), Speech))
	ended := requireEvent[SpeechEnded](t, segmenter.Flush())
	if ended.Utterance.Duration() != frameDuration {
		t.Fatalf("Duration() = %v, want %v", ended.Utterance.Duration(), frameDuration)
	}
}

func TestSegmenterRejectsMalformedFrameWithoutChangingState(t *testing.T) {
	segmenter := newTestSegmenter(t, Settings{
		Format: testFormat(), MinSpeech: frameDuration, EndSilence: 40 * time.Millisecond,
		MaxUtterance: time.Second,
	})
	start := time.Unix(100, 0)
	bad := audio.Frame{Samples: []int16{1}, CapturedAt: start}
	if _, err := segmenter.Process(bad, Speech); err == nil {
		t.Fatal("Process() succeeded, want malformed frame error")
	}

	requireEvent[SpeechStarted](t, process(t, segmenter, testFrame(start, 2), Speech))
	ended := requireEvent[SpeechEnded](t, segmenter.Flush())
	if got := ended.Utterance.Samples[0]; got != 2 {
		t.Fatalf("first sample = %d, malformed frame changed state", got)
	}
}

func newTestSegmenter(t *testing.T, settings Settings) *Segmenter {
	t.Helper()
	segmenter, err := NewSegmenter(settings)
	if err != nil {
		t.Fatalf("NewSegmenter() error: %v", err)
	}
	return segmenter
}

func testFormat() audio.Format {
	return audio.Format{SampleRate: 16000, Channels: 1, FrameDuration: frameDuration}
}

func testFrame(at time.Time, value int16) audio.Frame {
	samples := make([]int16, testFormat().SamplesPerFrame())
	for index := range samples {
		samples[index] = value
	}
	return audio.Frame{Samples: samples, CapturedAt: at}
}

func process(t *testing.T, segmenter *Segmenter, frame audio.Frame, activity Activity) []Event {
	t.Helper()
	events, err := segmenter.Process(frame, activity)
	if err != nil {
		t.Fatalf("Process() error: %v", err)
	}
	return events
}

func processNoEvents(t *testing.T, segmenter *Segmenter, frame audio.Frame, activity Activity) {
	t.Helper()
	if events := process(t, segmenter, frame, activity); len(events) != 0 {
		t.Fatalf("Process() events = %v, want none", events)
	}
}

func requireEvent[T Event](t *testing.T, events []Event) T {
	t.Helper()
	if len(events) != 1 {
		t.Fatalf("events count = %d, want 1", len(events))
	}
	event, ok := events[0].(T)
	if !ok {
		t.Fatalf("event type = %T, want requested type", events[0])
	}
	return event
}

package listener

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/audio"
	"github.com/Seraf-seraf/voice_assistent/internal/vad"
)

func TestListenerPublishesSpeechEvents(t *testing.T) {
	format := testFormat()
	source := newFakeSource(4, nil)
	detector := &fakeDetector{}
	segmenter := newSegmenter(t, format)
	listener := newListener(t, source, detector, segmenter)
	ctx, cancel := context.WithCancel(context.Background())
	runResult := runListener(listener, ctx)
	start := time.Unix(100, 0)

	source.frames <- testFrame(format, start, 1)
	started := receiveEvent[vad.SpeechStarted](t, listener.Events())
	if !started.At.Equal(start) {
		t.Fatalf("SpeechStarted.At = %v, want %v", started.At, start)
	}
	source.frames <- testFrame(format, start.Add(format.FrameDuration), 0)
	ended := receiveEvent[vad.SpeechEnded](t, listener.Events())
	if ended.Utterance.ID != 1 {
		t.Fatalf("Utterance.ID = %d, want 1", ended.Utterance.ID)
	}

	cancel()
	if err := receiveRunResult(t, runResult); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if detector.closeCalls != 1 || source.closeCalls != 1 {
		t.Fatalf("close calls: detector=%d source=%d", detector.closeCalls, source.closeCalls)
	}
}

func TestListenerReturnsDetectorErrorAndStopsSource(t *testing.T) {
	wantErr := errors.New("ошибка detector")
	source := newFakeSource(1, nil)
	detector := &fakeDetector{classifyErr: wantErr}
	listener := newListener(t, source, detector, newSegmenter(t, testFormat()))
	runResult := runListener(listener, context.Background())

	source.frames <- testFrame(testFormat(), time.Now(), 1)
	err := receiveRunResult(t, runResult)
	if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "классифицировать") {
		t.Fatalf("Run() error = %v, want detector error", err)
	}
	if source.closeCalls != 1 || detector.closeCalls != 1 {
		t.Fatalf("dependencies not closed: source=%d detector=%d", source.closeCalls, detector.closeCalls)
	}
}

func TestListenerReturnsSourceError(t *testing.T) {
	wantErr := errors.New("микрофон недоступен")
	source := newFakeSource(1, wantErr)
	listener := newListener(t, source, &fakeDetector{}, newSegmenter(t, testFormat()))

	err := listener.Run(context.Background())
	if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "получить audio frames") {
		t.Fatalf("Run() error = %v, want source error", err)
	}
}

func TestListenerCanRunOnlyOnce(t *testing.T) {
	source := newFakeSource(1, nil)
	listener := newListener(t, source, &fakeDetector{}, newSegmenter(t, testFormat()))
	if err := source.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	if err := listener.Run(context.Background()); err != nil {
		t.Fatalf("first Run() error: %v", err)
	}
	if err := listener.Run(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("second Run() error = %v, want ErrClosed", err)
	}
}

type fakeSource struct {
	frames     chan audio.Frame
	runErr     error
	stop       chan struct{}
	closeOnce  sync.Once
	closeCalls int
}

func newFakeSource(buffer int, runErr error) *fakeSource {
	return &fakeSource{frames: make(chan audio.Frame, buffer), runErr: runErr, stop: make(chan struct{})}
}

func (s *fakeSource) Run(ctx context.Context) error {
	if s.runErr != nil {
		return s.runErr
	}
	select {
	case <-ctx.Done():
		return nil
	case <-s.stop:
		return nil
	}
}

func (s *fakeSource) Frames() <-chan audio.Frame { return s.frames }
func (s *fakeSource) DroppedFrames() uint64      { return 0 }

func (s *fakeSource) Close() error {
	s.closeOnce.Do(func() {
		s.closeCalls++
		close(s.stop)
	})
	return nil
}

type fakeDetector struct {
	classifyErr error
	closeCalls  int
}

func (d *fakeDetector) Classify(frame audio.Frame) (vad.Activity, error) {
	if d.classifyErr != nil {
		return vad.Silence, d.classifyErr
	}
	if frame.Samples[0] == 0 {
		return vad.Silence, nil
	}
	return vad.Speech, nil
}

func (d *fakeDetector) Close() error {
	d.closeCalls++
	return nil
}

func newListener(t *testing.T, source *fakeSource, detector *fakeDetector, segmenter *vad.Segmenter) *Listener {
	t.Helper()
	listener, err := New(source, detector, segmenter, 2)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	return listener
}

func newSegmenter(t *testing.T, format audio.Format) *vad.Segmenter {
	t.Helper()
	segmenter, err := vad.NewSegmenter(vad.Settings{
		Format: format, MinSpeech: format.FrameDuration, EndSilence: format.FrameDuration,
		MaxUtterance: time.Second,
	})
	if err != nil {
		t.Fatalf("NewSegmenter() error: %v", err)
	}
	return segmenter
}

func testFormat() audio.Format {
	return audio.Format{SampleRate: 16000, Channels: 1, FrameDuration: 20 * time.Millisecond}
}

func testFrame(format audio.Format, capturedAt time.Time, value int16) audio.Frame {
	samples := make([]int16, format.SamplesPerFrame())
	for index := range samples {
		samples[index] = value
	}
	return audio.Frame{Samples: samples, CapturedAt: capturedAt}
}

func runListener(listener *Listener, ctx context.Context) <-chan error {
	result := make(chan error, 1)
	go func() { result <- listener.Run(ctx) }()
	return result
}

func receiveRunResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(time.Second):
		t.Fatal("Listener.Run did not return")
		return nil
	}
}

func receiveEvent[T vad.Event](t *testing.T, events <-chan vad.Event) T {
	t.Helper()
	select {
	case event := <-events:
		value, ok := event.(T)
		if !ok {
			t.Fatalf("event type = %T, want requested type", event)
		}
		return value
	case <-time.After(time.Second):
		t.Fatal("event not received")
		var zero T
		return zero
	}
}

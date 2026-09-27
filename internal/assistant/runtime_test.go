package assistant

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/audio"
	"github.com/Seraf-seraf/voice_assistent/internal/stt"
	"github.com/Seraf-seraf/voice_assistent/internal/vad"
)

type fakeListener struct {
	events    chan vad.Event
	run       func(context.Context) error
	started   chan struct{}
	startOnce sync.Once
}

func newFakeListener(run func(context.Context) error, events ...vad.Event) *fakeListener {
	channel := make(chan vad.Event, len(events))
	for _, event := range events {
		channel <- event
	}
	return &fakeListener{events: channel, run: run, started: make(chan struct{})}
}
func (l *fakeListener) Run(ctx context.Context) error {
	l.startOnce.Do(func() { close(l.started) })
	defer close(l.events)
	return l.run(ctx)
}
func (l *fakeListener) Events() <-chan vad.Event { return l.events }

type fakePipeline struct {
	handle    func(context.Context, vad.Event) error
	run       func(context.Context) error
	closeOnce sync.Once
	closed    chan struct{}
	started   chan struct{}
}

func newFakePipeline() *fakePipeline {
	return &fakePipeline{closed: make(chan struct{}), started: make(chan struct{})}
}
func (p *fakePipeline) Handle(ctx context.Context, event vad.Event) error {
	return p.handle(ctx, event)
}
func (p *fakePipeline) Run(ctx context.Context) error {
	close(p.started)
	if p.run != nil {
		return p.run(ctx)
	}
	<-p.closed
	return nil
}
func (p *fakePipeline) CloseInput() { p.closeOnce.Do(func() { close(p.closed) }) }

func TestRuntimeDrainsEventsAndClosesPipelineInput(t *testing.T) {
	first, second := vad.SpeechStarted{}, vad.SpeechEnded{}
	listener := newFakeListener(func(context.Context) error { return nil }, first, second)
	pipeline := newFakePipeline()
	var received []vad.Event
	pipeline.handle = func(_ context.Context, event vad.Event) error { received = append(received, event); return nil }
	runtime, err := NewRuntime(listener, pipeline)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(received, []vad.Event{first, second}) {
		t.Fatalf("received=%#v", received)
	}
	select {
	case <-pipeline.closed:
	default:
		t.Fatal("pipeline input was not closed")
	}
}

func TestRuntimeWaitsForQueuedPipelineWorkAfterListenerEOF(t *testing.T) {
	listener := newFakeListener(func(context.Context) error { return nil }, vad.SpeechEnded{})
	pipeline := newFakePipeline()
	workDone := make(chan struct{})
	pipeline.handle = func(context.Context, vad.Event) error { return nil }
	pipeline.run = func(ctx context.Context) error {
		<-pipeline.closed
		close(workDone)
		return nil
	}
	runtime, err := NewRuntime(listener, pipeline)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-workDone:
	default:
		t.Fatal("Runtime returned before queued work completed")
	}
}

func TestRuntimeRunsPipelineAndListenerUntilParentCancellation(t *testing.T) {
	listener := newFakeListener(func(ctx context.Context) error { <-ctx.Done(); return nil })
	pipeline := newFakePipeline()
	pipeline.run = func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
	runtime, err := NewRuntime(listener, pipeline)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	<-listener.started
	<-pipeline.started
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() stuck")
	}
}

func TestRuntimePipelineErrorCancelsListenerAndJoinsCleanupError(t *testing.T) {
	pipelineErr, listenerErr := errors.New("pipeline failed"), errors.New("listener cleanup failed")
	listener := newFakeListener(func(ctx context.Context) error { <-ctx.Done(); return listenerErr })
	pipeline := newFakePipeline()
	pipeline.run = func(context.Context) error { return pipelineErr }
	runtime, err := NewRuntime(listener, pipeline)
	if err != nil {
		t.Fatal(err)
	}
	err = runtime.Run(context.Background())
	if !errors.Is(err, pipelineErr) || !errors.Is(err, listenerErr) {
		t.Fatalf("Run()=%v", err)
	}
}

func TestRuntimeListenerErrorCancelsPipeline(t *testing.T) {
	listenerErr := errors.New("listener failed")
	listener := newFakeListener(func(context.Context) error { return listenerErr })
	pipeline := newFakePipeline()
	pipeline.run = func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
	runtime, err := NewRuntime(listener, pipeline)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Run(context.Background()); !errors.Is(err, listenerErr) {
		t.Fatalf("Run()=%v", err)
	}
}

func TestRuntimePreservesPipelineLocalDeadline(t *testing.T) {
	listener := newFakeListener(func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
	pipeline := newFakePipeline()
	pipeline.run = func(context.Context) error { return context.DeadlineExceeded }
	runtime, err := NewRuntime(listener, pipeline)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Run(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run()=%v, want local model deadline", err)
	}
}

func TestRuntimeHandleErrorCancelsAndJoinsIndependentErrors(t *testing.T) {
	handleErr, pipelineErr, listenerErr := errors.New("handle failed"), errors.New("pipeline shutdown"), errors.New("listener shutdown")
	listener := newFakeListener(func(ctx context.Context) error { <-ctx.Done(); return listenerErr }, vad.SpeechStarted{})
	pipeline := newFakePipeline()
	pipeline.handle = func(context.Context, vad.Event) error { return handleErr }
	pipeline.run = func(ctx context.Context) error { <-ctx.Done(); return pipelineErr }
	runtime, err := NewRuntime(listener, pipeline)
	if err != nil {
		t.Fatal(err)
	}
	err = runtime.Run(context.Background())
	for _, want := range []error{handleErr, pipelineErr, listenerErr} {
		if !errors.Is(err, want) {
			t.Fatalf("Run()=%v missing %v", err, want)
		}
	}
}

func TestNewRuntimeRejectsNilDependencies(t *testing.T) {
	listener := newFakeListener(func(context.Context) error { return nil })
	if _, err := NewRuntime(nil, newFakePipeline()); err == nil {
		t.Fatal("nil listener accepted")
	}
	if _, err := NewRuntime(listener, nil); err == nil {
		t.Fatal("nil pipeline accepted")
	}
	var nilListener *fakeListener
	if _, err := NewRuntime(nilListener, newFakePipeline()); err == nil {
		t.Fatal("typed nil listener accepted")
	}
	var nilPipeline *fakePipeline
	if _, err := NewRuntime(listener, nilPipeline); err == nil {
		t.Fatal("typed nil pipeline accepted")
	}
}

type blockedDeliveryPipeline struct {
	*Transcriber
	deliveryEntered chan struct{}
	thirdID         uint64
}

func (p *blockedDeliveryPipeline) Handle(ctx context.Context, event vad.Event) error {
	targetID := p.thirdID
	if targetID == 0 {
		targetID = 3
	}
	if ended, ok := event.(vad.SpeechEnded); ok && ended.Utterance.ID == targetID {
		close(p.deliveryEntered)
		<-ctx.Done()
		return ctx.Err()
	}
	return p.Transcriber.Handle(ctx, event)
}

func TestRuntimePipelineFailureUnblocksBlockedDelivery(t *testing.T) {
	pipelineErr := errors.New("whisper failed")
	transcribeStarted := make(chan struct{})
	releaseTranscribe := make(chan struct{})
	transcriber, err := NewTranscriber(fakeSTTClient{transcribe: func(context.Context, audio.Utterance) (stt.Transcript, error) {
		close(transcribeStarted)
		<-releaseTranscribe
		return stt.Transcript{}, pipelineErr
	}}, func(context.Context, Transcription) error { return nil }, 3)
	if err != nil {
		t.Fatal(err)
	}
	pipeline := &blockedDeliveryPipeline{Transcriber: transcriber, deliveryEntered: make(chan struct{})}
	listener := newFakeListener(func(context.Context) error { return nil },
		vad.SpeechEnded{Utterance: audio.Utterance{ID: 1}},
		vad.SpeechEnded{Utterance: audio.Utterance{ID: 2}},
		vad.SpeechEnded{Utterance: audio.Utterance{ID: 3}},
	)
	runtime, err := NewRuntime(listener, pipeline)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	select {
	case <-transcribeStarted:
	case <-time.After(time.Second):
		close(releaseTranscribe)
		cancel()
		waitRuntime(t, done)
		t.Fatal("STT did not start")
	}
	select {
	case <-pipeline.deliveryEntered:
	case <-time.After(time.Second):
		close(releaseTranscribe)
		cancel()
		waitRuntime(t, done)
		t.Fatal("third event was not delivered")
	}
	close(releaseTranscribe)
	select {
	case err := <-done:
		if !errors.Is(err, pipelineErr) {
			t.Fatalf("Run()=%v, want pipeline failure", err)
		}
	case <-time.After(time.Second):
		cancel()
		waitRuntime(t, done)
		t.Fatal("Runtime не остановил заблокированную доставку события после ошибки конвейера")
	}
}

func TestRuntimeParentCancellationUnblocksBlockedDelivery(t *testing.T) {
	transcribeStarted := make(chan struct{})
	transcriber, err := NewTranscriber(fakeSTTClient{transcribe: func(ctx context.Context, _ audio.Utterance) (stt.Transcript, error) {
		close(transcribeStarted)
		<-ctx.Done()
		return stt.Transcript{}, ctx.Err()
	}}, func(context.Context, Transcription) error { return nil }, 3)
	if err != nil {
		t.Fatal(err)
	}
	pipeline := &blockedDeliveryPipeline{Transcriber: transcriber, deliveryEntered: make(chan struct{}), thirdID: 13}
	listener := newFakeListener(func(context.Context) error { return nil },
		vad.SpeechEnded{Utterance: audio.Utterance{ID: 11}},
		vad.SpeechEnded{Utterance: audio.Utterance{ID: 12}},
		vad.SpeechEnded{Utterance: audio.Utterance{ID: 13}},
	)
	runtime, err := NewRuntime(listener, pipeline)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	select {
	case <-transcribeStarted:
	case <-time.After(time.Second):
		cancel()
		waitRuntime(t, done)
		t.Fatal("STT did not start")
	}
	select {
	case <-pipeline.deliveryEntered:
	case <-time.After(time.Second):
		cancel()
		waitRuntime(t, done)
		t.Fatal("third event was not delivered")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("pure parent cancellation returned %v", err)
		}
	case <-time.After(time.Second):
		cancel()
		waitRuntime(t, done)
		t.Fatal("Runtime remained blocked after parent cancellation")
	}
}

type gatedFailingListener struct {
	events  chan vad.Event
	release chan struct{}
	err     error
}

func (l *gatedFailingListener) Run(ctx context.Context) error {
	defer close(l.events)
	select {
	case <-l.release:
		return l.err
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (l *gatedFailingListener) Events() <-chan vad.Event { return l.events }

func waitRuntime(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Runtime goroutine did not exit after cancellation")
	}
}

func TestRuntimeListenerFailureUnblocksBlockedDelivery(t *testing.T) {
	listenerErr := errors.New("listener failed while closing")
	transcribeStarted := make(chan struct{})
	transcriber, err := NewTranscriber(fakeSTTClient{transcribe: func(ctx context.Context, _ audio.Utterance) (stt.Transcript, error) {
		close(transcribeStarted)
		<-ctx.Done()
		return stt.Transcript{}, ctx.Err()
	}}, func(context.Context, Transcription) error { return nil }, 3)
	if err != nil {
		t.Fatal(err)
	}
	thirdHandle := make(chan struct{})
	pipeline := &blockedDeliveryPipeline{Transcriber: transcriber, deliveryEntered: thirdHandle}
	listener := &gatedFailingListener{
		events: make(chan vad.Event, 3), release: make(chan struct{}), err: listenerErr,
	}
	for id := uint64(1); id <= 3; id++ {
		listener.events <- vad.SpeechEnded{Utterance: audio.Utterance{ID: id}}
	}
	runtime, err := NewRuntime(listener, pipeline)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	select {
	case <-transcribeStarted:
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("STT did not start")
	}
	select {
	case <-thirdHandle:
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("third event was not delivered")
	}
	close(listener.release)
	select {
	case err := <-done:
		if !errors.Is(err, listenerErr) {
			t.Fatalf("Run()=%v, want listener error", err)
		}
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("listener failure did not unblock event delivery")
	}
}

func TestRuntimeDrainsRealTranscriberAtListenerEOF(t *testing.T) {
	var received []uint64
	transcriber, err := NewTranscriber(fakeSTTClient{transcribe: func(_ context.Context, utterance audio.Utterance) (stt.Transcript, error) {
		return stt.Transcript{Text: "ok"}, nil
	}}, func(_ context.Context, result Transcription) error {
		received = append(received, result.UtteranceID)
		return nil
	}, 3)
	if err != nil {
		t.Fatal(err)
	}
	listener := newFakeListener(func(context.Context) error { return nil },
		vad.SpeechEnded{Utterance: audio.Utterance{ID: 21}},
		vad.SpeechEnded{Utterance: audio.Utterance{ID: 22}},
		vad.SpeechEnded{Utterance: audio.Utterance{ID: 23}},
	)
	runtime, err := NewRuntime(listener, transcriber)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(received, []uint64{21, 22, 23}) {
		t.Fatalf("received=%v", received)
	}
}

func TestCleanupErrorSuppressesOnlyPureSharedCancellation(t *testing.T) {
	cleanupFailure := errors.New("cleanup failed")
	if got := cleanupError("component", errors.Join(context.Canceled, cleanupFailure), context.Canceled); !errors.Is(got, cleanupFailure) {
		t.Fatalf("mixed cancellation lost cleanup failure: %v", got)
	}
	if got := cleanupError("component", fmt.Errorf("local timeout: %w", context.DeadlineExceeded), context.Canceled); !errors.Is(got, context.DeadlineExceeded) {
		t.Fatalf("local deadline suppressed: %v", got)
	}
	if got := cleanupError("component", fmt.Errorf("shutdown: %w", context.Canceled), context.Canceled); got != nil {
		t.Fatalf("pure shared cancellation returned %v", got)
	}
}

func TestCancellationOnlyRequiresEveryLeafToBeAllowed(t *testing.T) {
	sentinel := errors.New("ошибка очистки")
	for _, test := range []struct {
		name    string
		err     error
		allowed []error
		want    bool
	}{
		{name: "wrapped allowed cause", err: fmt.Errorf("обёртка: %w", context.Canceled), allowed: []error{context.Canceled}, want: true},
		{name: "mixed allowed causes", err: errors.Join(context.Canceled, ErrSpeechInterrupted), allowed: []error{context.Canceled, ErrSpeechInterrupted}, want: true},
		{name: "independent joined error", err: errors.Join(context.Canceled, sentinel), allowed: []error{context.Canceled, ErrSpeechInterrupted}},
		{name: "deadline is not cancellation", err: context.DeadlineExceeded, allowed: []error{context.Canceled, ErrSpeechInterrupted}},
		{name: "nil error", allowed: []error{context.Canceled}},
		{name: "no allowed causes", err: context.Canceled},
		{name: "only nil allowed causes", err: context.Canceled, allowed: []error{nil}},
		{name: "empty error tree", err: emptyErrorTree{}, allowed: []error{context.Canceled}},
		{name: "error tree without nonnil leaves", err: nilErrorTree{}, allowed: []error{context.Canceled}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := cancellationOnly(test.err, test.allowed...); got != test.want {
				t.Fatalf("cancellationOnly(%v)=%v, want %v", test.err, got, test.want)
			}
		})
	}
}

type emptyErrorTree struct{}

func (emptyErrorTree) Error() string   { return "пустое дерево ошибок" }
func (emptyErrorTree) Unwrap() []error { return nil }

type nilErrorTree struct{}

func (nilErrorTree) Error() string   { return "дерево без причин" }
func (nilErrorTree) Unwrap() []error { return []error{nil} }

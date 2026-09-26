package assistant

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

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

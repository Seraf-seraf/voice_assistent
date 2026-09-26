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
	eventChannel := make(chan vad.Event, len(events))
	for _, event := range events {
		eventChannel <- event
	}
	return &fakeListener{events: eventChannel, run: run, started: make(chan struct{})}
}

func (l *fakeListener) Run(ctx context.Context) error {
	l.startOnce.Do(func() { close(l.started) })
	err := l.run(ctx)
	close(l.events)
	return err
}

func (l *fakeListener) Events() <-chan vad.Event {
	return l.events
}

func TestRuntimeDeliversQueuedEventsInOrderBeforeListenerReturns(t *testing.T) {
	first := vad.SpeechStarted{}
	second := vad.SpeechEnded{}
	listener := newFakeListener(func(context.Context) error { return nil }, first, second)
	var received []vad.Event
	runtime, err := NewRuntime(listener, func(_ context.Context, event vad.Event) error {
		received = append(received, event)
		return nil
	})
	if err != nil {
		t.Fatalf("NewRuntime() error: %v", err)
	}
	if err := runtime.Run(context.Background()); err != nil {
		t.Fatalf("Runtime.Run() error: %v", err)
	}
	if !reflect.DeepEqual(received, []vad.Event{first, second}) {
		t.Fatalf("received events = %#v, want original order", received)
	}
}

func TestRuntimeStartsListenerAndStopsOnContextCancellation(t *testing.T) {
	listener := newFakeListener(func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	}, vad.SpeechStarted{})
	handlerStarted := make(chan struct{})
	runtime, err := NewRuntime(listener, func(ctx context.Context, _ vad.Event) error {
		close(handlerStarted)
		<-ctx.Done()
		return ctx.Err()
	})
	if err != nil {
		t.Fatalf("NewRuntime() error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- runtime.Run(ctx) }()
	select {
	case <-listener.started:
	case <-time.After(time.Second):
		t.Fatal("listener.Run() was not called")
	}
	select {
	case <-handlerStarted:
	case <-time.After(time.Second):
		t.Fatal("runtime handler was not called")
	}
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Runtime.Run() error after cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Runtime.Run() did not stop after cancellation")
	}
}

func TestRuntimeReturnsListenerError(t *testing.T) {
	wantErr := errors.New("listener failed")
	listener := newFakeListener(func(context.Context) error { return wantErr })
	runtime, err := NewRuntime(listener, func(context.Context, vad.Event) error { return nil })
	if err != nil {
		t.Fatalf("NewRuntime() error: %v", err)
	}
	if err := runtime.Run(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("Runtime.Run() error = %v, want wrapped listener error", err)
	}
}

func TestRuntimeHandlerErrorCancelsListenerAndJoinsListenerError(t *testing.T) {
	handlerErr := errors.New("handler failed")
	listenerErr := errors.New("listener shutdown failed")
	listener := newFakeListener(func(ctx context.Context) error {
		<-ctx.Done()
		return listenerErr
	}, vad.SpeechStarted{})
	runtime, err := NewRuntime(listener, func(context.Context, vad.Event) error { return handlerErr })
	if err != nil {
		t.Fatalf("NewRuntime() error: %v", err)
	}
	if err := runtime.Run(context.Background()); !errors.Is(err, handlerErr) || !errors.Is(err, listenerErr) {
		t.Fatalf("Runtime.Run() error = %v, want both handler and listener errors", err)
	}
}

func TestNewRuntimeRejectsNilDependencies(t *testing.T) {
	if _, err := NewRuntime(nil, func(context.Context, vad.Event) error { return nil }); err == nil {
		t.Fatal("NewRuntime(nil, handler) succeeded")
	}
	listener := newFakeListener(func(context.Context) error { return nil })
	if _, err := NewRuntime(listener, nil); err == nil {
		t.Fatal("NewRuntime(listener, nil) succeeded")
	}
	var nilListener *fakeListener
	if _, err := NewRuntime(nilListener, func(context.Context, vad.Event) error { return nil }); err == nil {
		t.Fatal("NewRuntime(typed nil listener, handler) succeeded")
	}
}

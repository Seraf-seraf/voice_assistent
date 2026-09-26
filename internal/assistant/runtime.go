package assistant

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/Seraf-seraf/voice_assistent/internal/vad"
)

// SpeechListener запускает обработку и закрывает Events после завершения Run.
type SpeechListener interface {
	Run(context.Context) error
	Events() <-chan vad.Event
}

type SpeechEventHandler func(context.Context, vad.Event) error

type Runtime struct {
	listener SpeechListener
	handler  SpeechEventHandler
}

func NewRuntime(listener SpeechListener, handler SpeechEventHandler) (*Runtime, error) {
	if listener == nil || isNilListener(listener) {
		return nil, errors.New("speech listener обязателен")
	}
	if handler == nil {
		return nil, errors.New("speech event handler обязателен")
	}
	return &Runtime{listener: listener, handler: handler}, nil
}

func isNilListener(listener SpeechListener) bool {
	value := reflect.ValueOf(listener)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func (r *Runtime) Run(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	listenerDone := make(chan error, 1)
	go func() {
		listenerDone <- r.listener.Run(ctx)
	}()

	events := r.listener.Events()
	listenerDoneChannel := (<-chan error)(listenerDone)
	parentDone := parent.Done()
	var listenerErr error
	var handlerErr error
	listenerFinished := false

	for {
		if listenerFinished && events == nil {
			return runtimeError(listenerErr, handlerErr)
		}

		select {
		case event, open := <-events:
			if !open {
				events = nil
				continue
			}
			if err := r.handler(ctx, event); err != nil {
				if parent.Err() == nil || !errors.Is(err, parent.Err()) {
					handlerErr = errors.Join(handlerErr, err)
				}
				cancel()
			}
		case err := <-listenerDoneChannel:
			listenerErr = err
			listenerFinished = true
			listenerDoneChannel = nil
		case <-parentDone:
			cancel()
			if listenerDoneChannel != nil {
				listenerErr = <-listenerDoneChannel
				listenerFinished = true
				listenerDoneChannel = nil
			}
			parentDone = nil
		}
	}
}

func runtimeError(listenerErr, handlerErr error) error {
	if listenerErr != nil {
		listenerErr = fmt.Errorf("запустить speech listener: %w", listenerErr)
	}
	if handlerErr != nil {
		handlerErr = fmt.Errorf("обработать speech event: %w", handlerErr)
	}
	return errors.Join(handlerErr, listenerErr)
}

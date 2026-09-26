package assistant

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/Seraf-seraf/voice_assistent/internal/vad"
)

type SpeechListener interface {
	Run(context.Context) error
	Events() <-chan vad.Event
}

type SpeechPipeline interface {
	Handle(context.Context, vad.Event) error
	Run(context.Context) error
	CloseInput()
}

type Runtime struct {
	listener SpeechListener
	pipeline SpeechPipeline
}

func NewRuntime(listener SpeechListener, pipeline SpeechPipeline) (*Runtime, error) {
	if listener == nil || isNilDependency(listener) {
		return nil, errors.New("speech listener обязателен")
	}
	if pipeline == nil || isNilDependency(pipeline) {
		return nil, errors.New("speech pipeline обязателен")
	}
	return &Runtime{listener: listener, pipeline: pipeline}, nil
}

func isNilDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func (r *Runtime) Run(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	listenerDone := make(chan error, 1)
	pipelineDone := make(chan error, 1)
	go func() { listenerDone <- r.listener.Run(ctx) }()
	go func() { pipelineDone <- r.pipeline.Run(ctx) }()

	listenerChannel, pipelineChannel := (<-chan error)(listenerDone), (<-chan error)(pipelineDone)
	events := r.listener.Events()
	var listenerErr, pipelineErr error
	for {
		select {
		case <-parent.Done():
			cancel()
			if listenerChannel != nil {
				listenerErr = <-listenerChannel
			}
			if pipelineChannel != nil {
				pipelineErr = <-pipelineChannel
			}
			return errors.Join(cleanupError("speech listener", listenerErr, parent.Err()), cleanupError("speech pipeline", pipelineErr, parent.Err()))
		case event, open := <-events:
			if !open {
				events = nil
				if listenerChannel != nil {
					listenerErr = <-listenerChannel
					listenerChannel = nil
					if listenerErr != nil {
						cancel()
						if pipelineChannel != nil {
							pipelineErr = <-pipelineChannel
						}
						return errors.Join(wrapError("speech listener", listenerErr), wrapError("speech pipeline", pipelineErr))
					}
				}
				r.pipeline.CloseInput()
				if pipelineChannel != nil {
					pipelineErr = <-pipelineChannel
				}
				return wrapError("speech pipeline", pipelineErr)
			}
			if err := r.pipeline.Handle(ctx, event); err != nil {
				cancel()
				if listenerChannel != nil {
					listenerErr = <-listenerChannel
				}
				if pipelineChannel != nil {
					pipelineErr = <-pipelineChannel
				}
				return errors.Join(fmt.Errorf("передать speech event: %w", err), wrapError("speech listener", listenerErr), wrapError("speech pipeline", pipelineErr))
			}
		case err := <-listenerChannel:
			listenerChannel = nil
			listenerErr = err
			if err != nil {
				cancel()
				if pipelineChannel != nil {
					pipelineErr = <-pipelineChannel
				}
				return errors.Join(wrapError("speech listener", err), wrapError("speech pipeline", pipelineErr))
			}
			// Events are closed by the listener after Run returns; keep draining them.
		case err := <-pipelineChannel:
			pipelineChannel = nil
			pipelineErr = err
			if err != nil {
				cancel()
				if listenerChannel != nil {
					listenerErr = <-listenerChannel
				}
				return errors.Join(wrapError("speech pipeline", err), wrapError("speech listener", listenerErr))
			}
			// A normally completed pipeline cannot accept further events.
			cancel()
			if listenerChannel != nil {
				listenerErr = <-listenerChannel
			}
			return wrapError("speech listener", listenerErr)
		}
	}
}

func cleanupError(name string, err, cancellation error) error {
	if err == nil || errors.Is(err, cancellation) {
		return nil
	}
	return wrapError(name, err)
}

func wrapError(name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", name, err)
}

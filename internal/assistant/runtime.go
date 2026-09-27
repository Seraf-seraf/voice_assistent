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
		return nil, errors.New("слушатель речи обязателен")
	}
	if pipeline == nil || isNilDependency(pipeline) {
		return nil, errors.New("конвейер обработки речи обязателен")
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
	go func() {
		err := r.listener.Run(ctx)
		if err != nil {
			cancel()
		}
		listenerDone <- err
	}()
	go func() {
		err := r.pipeline.Run(ctx)
		cancel()
		pipelineDone <- err
	}()

	listenerChannel, pipelineChannel := (<-chan error)(listenerDone), (<-chan error)(pipelineDone)
	events := r.listener.Events()
	var listenerErr, pipelineErr error
	for {
		select {
		case <-ctx.Done():
			cancel()
			listenerErr, listenerChannel = awaitResult(listenerChannel, listenerErr)
			pipelineErr, pipelineChannel = awaitResult(pipelineChannel, pipelineErr)
			cancellation := ctx.Err()
			return errors.Join(cleanupError("speech listener", listenerErr, cancellation), cleanupError("speech pipeline", pipelineErr, cancellation))
		case event, open := <-events:
			if !open {
				events = nil
				if listenerChannel != nil {
					listenerErr = <-listenerChannel
					listenerChannel = nil
					if listenerErr != nil {
						cancel()
						pipelineErr, pipelineChannel = awaitResult(pipelineChannel, pipelineErr)
						return errors.Join(wrapError("speech listener", listenerErr), wrapError("speech pipeline", pipelineErr))
					}
				}
				r.pipeline.CloseInput()
				pipelineErr, pipelineChannel = awaitResult(pipelineChannel, pipelineErr)
				return wrapError("speech pipeline", pipelineErr)
			}
			if ctx.Err() != nil {
				listenerErr, listenerChannel = awaitResult(listenerChannel, listenerErr)
				pipelineErr, pipelineChannel = awaitResult(pipelineChannel, pipelineErr)
				cancellation := ctx.Err()
				return errors.Join(cleanupError("speech listener", listenerErr, cancellation), cleanupError("speech pipeline", pipelineErr, cancellation))
			}
			if err := r.pipeline.Handle(ctx, event); err != nil {
				wasCancelled := ctx.Err() != nil
				cancel()
				listenerErr, listenerChannel = awaitResult(listenerChannel, listenerErr)
				pipelineErr, pipelineChannel = awaitResult(pipelineChannel, pipelineErr)
				if cancellation := ctx.Err(); wasCancelled && cancellation != nil {
					return errors.Join(
						cleanupError("передать speech event", err, cancellation),
						cleanupError("speech listener", listenerErr, cancellation),
						cleanupError("speech pipeline", pipelineErr, cancellation),
					)
				}
				return errors.Join(fmt.Errorf("передать событие речи: %w", err), wrapError("слушатель речи", listenerErr), wrapError("конвейер обработки речи", pipelineErr))
			}
		case err := <-listenerChannel:
			listenerChannel = nil
			listenerErr = err
			if err != nil {
				cancel()
				pipelineErr, pipelineChannel = awaitResult(pipelineChannel, pipelineErr)
				return errors.Join(wrapError("speech listener", err), wrapError("speech pipeline", pipelineErr))
			}
			// Events are closed by the listener after Run returns; keep draining them.
		case err := <-pipelineChannel:
			pipelineChannel = nil
			pipelineErr = err
			if err != nil {
				cancel()
				listenerErr, listenerChannel = awaitResult(listenerChannel, listenerErr)
				return errors.Join(wrapError("speech pipeline", err), wrapError("speech listener", listenerErr))
			}
			// A normally completed pipeline cannot accept further events.
			cancel()
			listenerErr, listenerChannel = awaitResult(listenerChannel, listenerErr)
			return wrapError("speech listener", listenerErr)
		}
	}
}

func cleanupError(name string, err, cancellation error) error {
	if err == nil || cancellationOnly(err, cancellation) {
		return nil
	}
	return wrapError(name, err)
}

func cancellationOnly(err, cancellation error) bool {
	if err == nil || cancellation == nil {
		return false
	}
	if many, ok := err.(interface{ Unwrap() []error }); ok {
		children := many.Unwrap()
		if len(children) == 0 {
			return err == cancellation
		}
		for _, child := range children {
			if child != nil && !cancellationOnly(child, cancellation) {
				return false
			}
		}
		return true
	}
	if one, ok := err.(interface{ Unwrap() error }); ok {
		child := one.Unwrap()
		if child != nil {
			return cancellationOnly(child, cancellation)
		}
	}
	return err == cancellation
}

func awaitResult(channel <-chan error, current error) (error, <-chan error) {
	if channel == nil {
		return current, nil
	}
	return <-channel, nil
}

func wrapError(name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", name, err)
}

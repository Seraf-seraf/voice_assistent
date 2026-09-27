package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"

	"github.com/Seraf-seraf/voice_assistent/internal/assistant"
)

type responseOutput struct {
	output      io.Writer
	active      bool
	utteranceID uint64
	turnID      uint64
}

var _ assistant.ResponseSink = (*responseOutput)(nil)

func newResponseOutput(output io.Writer) (*responseOutput, error) {
	if output == nil || isNilWriter(output) {
		return nil, errors.New("response output обязателен")
	}
	return &responseOutput{output: output}, nil
}

func (o *responseOutput) Push(ctx context.Context, delta assistant.ResponseDelta) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if delta.Text == "" {
		return errors.New("response delta не должен быть пустым")
	}
	if o.active {
		if delta.UtteranceID != o.utteranceID || delta.TurnID != o.turnID {
			return errors.New("response delta не совпадает с активным turn")
		}
	} else {
		o.active = true
		o.utteranceID = delta.UtteranceID
		o.turnID = delta.TurnID
		if err := writeExact(o.output, "Ассистент: "); err != nil {
			return fmt.Errorf("write response prefix: %w", err)
		}
	}
	if err := writeExact(o.output, delta.Text); err != nil {
		return fmt.Errorf("write response delta: %w", err)
	}
	return nil
}

func (o *responseOutput) Complete(ctx context.Context, response assistant.Response) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !o.active || response.UtteranceID != o.utteranceID || response.TurnID != o.turnID {
		return errors.New("response не совпадает с активным stream")
	}
	if err := writeExact(o.output, "\n"); err != nil {
		return fmt.Errorf("terminate response line: %w", err)
	}
	o.reset()
	return nil
}

func (o *responseOutput) Abort(_ context.Context, abort assistant.ResponseAbort) error {
	if !o.active {
		return nil
	}
	if abort.UtteranceID != o.utteranceID || abort.TurnID != o.turnID {
		return errors.New("response abort не совпадает с активным stream")
	}
	err := writeExact(o.output, "\n")
	o.reset()
	if err != nil {
		return fmt.Errorf("terminate aborted response line: %w", err)
	}
	return nil
}

func (o *responseOutput) reset() {
	o.active = false
	o.utteranceID = 0
	o.turnID = 0
}

func writeExact(output io.Writer, text string) error {
	written, err := io.WriteString(output, text)
	if err != nil {
		return err
	}
	if written != len(text) {
		return io.ErrShortWrite
	}
	return nil
}

func newTranscriptionOutput(output io.Writer) (assistant.TranscriptionHandler, error) {
	if output == nil || isNilWriter(output) {
		return nil, errors.New("transcription output обязателен")
	}
	return func(ctx context.Context, transcription assistant.Transcription) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := writeExact(output, "Вы: "+transcription.Text+"\n"); err != nil {
			return fmt.Errorf("write transcription: %w", err)
		}
		return nil
	}, nil
}

func isNilWriter(output io.Writer) bool {
	value := reflect.ValueOf(output)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

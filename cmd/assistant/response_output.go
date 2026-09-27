package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"

	"github.com/Seraf-seraf/voice_assistent/internal/assistant"
)

func newResponseHandler(output io.Writer) (assistant.ResponseHandler, error) {
	if output == nil || isNilWriter(output) {
		return nil, errors.New("response output обязателен")
	}
	return func(ctx context.Context, response assistant.Response) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		text := "Ассистент: " + response.Text + "\n"
		written, err := io.WriteString(output, text)
		if err != nil {
			return fmt.Errorf("write response: %w", err)
		}
		if written != len(text) {
			return fmt.Errorf("write response: %w", io.ErrShortWrite)
		}
		return nil
	}, nil
}

func newTranscriptionOutput(output io.Writer) (assistant.TranscriptionHandler, error) {
	if output == nil || isNilWriter(output) {
		return nil, errors.New("transcription output обязателен")
	}
	return func(ctx context.Context, transcription assistant.Transcription) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		text := "Вы: " + transcription.Text + "\n"
		written, err := io.WriteString(output, text)
		if err != nil {
			return fmt.Errorf("write transcription: %w", err)
		}
		if written != len(text) {
			return fmt.Errorf("write transcription: %w", io.ErrShortWrite)
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

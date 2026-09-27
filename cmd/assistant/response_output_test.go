package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/Seraf-seraf/voice_assistent/internal/assistant"
)

type shortResponseWriter struct{}

func (shortResponseWriter) Write(value []byte) (int, error) { return len(value) - 1, nil }

type errorResponseWriter struct{ err error }

func (w errorResponseWriter) Write([]byte) (int, error) { return 0, w.err }

func TestResponseHandlerWritesCompleteResponse(t *testing.T) {
	var output bytes.Buffer
	handler, err := newResponseHandler(&output)
	if err != nil {
		t.Fatal(err)
	}
	if err := handler(context.Background(), assistant.Response{Text: "Привет."}); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "Ассистент: Привет.\n"; got != want {
		t.Fatalf("output=%q want=%q", got, want)
	}
}

func TestResponseHandlerReportsWriterErrors(t *testing.T) {
	sentinel := errors.New("disk full")
	handler, err := newResponseHandler(errorResponseWriter{err: sentinel})
	if err != nil {
		t.Fatal(err)
	}
	if err := handler(context.Background(), assistant.Response{Text: "x"}); !errors.Is(err, sentinel) {
		t.Fatalf("handler error=%v", err)
	}
	handler, _ = newResponseHandler(shortResponseWriter{})
	if err := handler(context.Background(), assistant.Response{Text: "x"}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write error=%v", err)
	}
}

func TestResponseHandlerRejectsNilAndCanceledInputs(t *testing.T) {
	if _, err := newResponseHandler(nil); err == nil {
		t.Fatal("nil writer accepted")
	}
	var nilBuffer *bytes.Buffer
	if _, err := newResponseHandler(nilBuffer); err == nil {
		t.Fatal("typed nil writer accepted")
	}
	var output bytes.Buffer
	handler, _ := newResponseHandler(&output)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := handler(ctx, assistant.Response{Text: "secret"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("handler error=%v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("canceled response was written: %q", output.String())
	}
}

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

func TestResponseOutputWritesDeltasImmediately(t *testing.T) {
	var output bytes.Buffer
	sink, err := newResponseOutput(&output)
	if err != nil {
		t.Fatal(err)
	}
	first := assistant.ResponseDelta{UtteranceID: 42, TurnID: 1, Text: "При"}
	if err := sink.Push(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "Ассистент: При"; got != want {
		t.Fatalf("after first Push=%q want=%q", got, want)
	}
	if err := sink.Push(context.Background(), assistant.ResponseDelta{UtteranceID: 42, TurnID: 1, Text: "вет"}); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "Ассистент: Привет"; got != want {
		t.Fatalf("after second Push=%q want=%q", got, want)
	}
	if err := sink.Complete(context.Background(), assistant.Response{UtteranceID: 42, TurnID: 1, Text: "Привет"}); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "Ассистент: Привет\n"; got != want {
		t.Fatalf("after Complete=%q want=%q", got, want)
	}
}

func TestResponseOutputAbortTerminatesPartialLineAndResets(t *testing.T) {
	var output bytes.Buffer
	sink, err := newResponseOutput(&output)
	if err != nil {
		t.Fatal(err)
	}
	result, err := sink.Abort(context.Background(), assistant.ResponseAbort{UtteranceID: 1, TurnID: 1})
	if err != nil || result != (assistant.ResponseAbortResult{UtteranceID: 1, TurnID: 1}) || output.Len() != 0 {
		t.Fatalf("Abort without active stream: err=%v output=%q", err, output.String())
	}
	if err := sink.Push(context.Background(), assistant.ResponseDelta{UtteranceID: 1, TurnID: 1, Text: "часть"}); err != nil {
		t.Fatal(err)
	}
	result, err = sink.Abort(context.Background(), assistant.ResponseAbort{UtteranceID: 1, TurnID: 1})
	if err != nil || result != (assistant.ResponseAbortResult{UtteranceID: 1, TurnID: 1}) {
		t.Fatal(err)
	}
	if err := sink.Push(context.Background(), assistant.ResponseDelta{UtteranceID: 2, TurnID: 2, Text: "новый"}); err != nil {
		t.Fatal(err)
	}
	if err := sink.Complete(context.Background(), assistant.Response{UtteranceID: 2, TurnID: 2, Text: "новый"}); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "Ассистент: часть\nАссистент: новый\n"; got != want {
		t.Fatalf("output=%q want=%q", got, want)
	}
}

func TestResponseOutputRejectsMismatchedIDsWithoutResettingStream(t *testing.T) {
	var output bytes.Buffer
	sink, _ := newResponseOutput(&output)
	if err := sink.Push(context.Background(), assistant.ResponseDelta{UtteranceID: 1, TurnID: 7, Text: "ok"}); err != nil {
		t.Fatal(err)
	}
	if err := sink.Push(context.Background(), assistant.ResponseDelta{UtteranceID: 2, TurnID: 7, Text: "bad"}); err == nil {
		t.Fatal("Push accepted mismatched utterance ID")
	}
	if err := sink.Complete(context.Background(), assistant.Response{UtteranceID: 1, TurnID: 8, Text: "ok"}); err == nil {
		t.Fatal("Complete accepted mismatched turn ID")
	}
	if _, err := sink.Abort(context.Background(), assistant.ResponseAbort{UtteranceID: 1, TurnID: 8}); err == nil {
		t.Fatal("Abort accepted mismatched turn ID")
	}
	if err := sink.Push(context.Background(), assistant.ResponseDelta{UtteranceID: 1, TurnID: 7, Text: "!"}); err != nil {
		t.Fatalf("correct active stream was lost: %v", err)
	}
	if err := sink.Complete(context.Background(), assistant.Response{UtteranceID: 1, TurnID: 7, Text: "ok!"}); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "Ассистент: ok!\n"; got != want {
		t.Fatalf("output=%q want=%q", got, want)
	}
}

func TestResponseOutputCancellationDoesNotWrite(t *testing.T) {
	var output bytes.Buffer
	sink, _ := newResponseOutput(&output)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sink.Push(ctx, assistant.ResponseDelta{UtteranceID: 1, TurnID: 1, Text: "secret"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Push error=%v", err)
	}
	if err := sink.Complete(ctx, assistant.Response{UtteranceID: 1, TurnID: 1}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Complete error=%v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("canceled output wrote %q", output.String())
	}
}

func TestResponseOutputPreservesWriteErrorsAndShortWrites(t *testing.T) {
	sentinel := errors.New("disk full")
	for _, test := range []struct {
		name   string
		writer io.Writer
		want   error
	}{
		{name: "writer", writer: errorResponseWriter{err: sentinel}, want: sentinel},
		{name: "short write", writer: shortResponseWriter{}, want: io.ErrShortWrite},
	} {
		t.Run(test.name, func(t *testing.T) {
			sink, err := newResponseOutput(test.writer)
			if err != nil {
				t.Fatal(err)
			}
			err = sink.Push(context.Background(), assistant.ResponseDelta{UtteranceID: 1, TurnID: 1, Text: "x"})
			if !errors.Is(err, test.want) {
				t.Fatalf("Push error=%v want cause %v", err, test.want)
			}
		})
	}
}

func TestResponseOutputAbortResetsStateEvenWhenWriterFails(t *testing.T) {
	var output bytes.Buffer
	sink, _ := newResponseOutput(&output)
	if err := sink.Push(context.Background(), assistant.ResponseDelta{UtteranceID: 1, TurnID: 1, Text: "partial"}); err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("write failed")
	sink.output = errorResponseWriter{err: sentinel}
	if _, err := sink.Abort(context.Background(), assistant.ResponseAbort{UtteranceID: 1, TurnID: 1}); !errors.Is(err, sentinel) {
		t.Fatalf("Abort error=%v want writer cause", err)
	}
	sink.output = &output
	if err := sink.Push(context.Background(), assistant.ResponseDelta{UtteranceID: 2, TurnID: 2, Text: "next"}); err != nil {
		t.Fatalf("state was not reset after Abort failure: %v", err)
	}
	if err := sink.Complete(context.Background(), assistant.Response{UtteranceID: 2, TurnID: 2, Text: "next"}); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "Ассистент: partialАссистент: next\n"; got != want {
		t.Fatalf("output=%q want=%q", got, want)
	}
}

func TestResponseOutputRejectsNilAndEmptyDelta(t *testing.T) {
	if _, err := newResponseOutput(nil); err == nil {
		t.Fatal("nil writer accepted")
	}
	var nilBuffer *bytes.Buffer
	if _, err := newResponseOutput(nilBuffer); err == nil {
		t.Fatal("typed nil writer accepted")
	}
	var output bytes.Buffer
	sink, _ := newResponseOutput(&output)
	if err := sink.Push(context.Background(), assistant.ResponseDelta{UtteranceID: 1, TurnID: 1}); err == nil {
		t.Fatal("empty delta accepted")
	}
	if output.Len() != 0 {
		t.Fatalf("empty delta wrote %q", output.String())
	}
}

func TestTranscriptionOutputWritesRecognizedText(t *testing.T) {
	var output bytes.Buffer
	handler, err := newTranscriptionOutput(&output)
	if err != nil {
		t.Fatal(err)
	}
	if err := handler(context.Background(), assistant.Transcription{Text: "распознанная фраза"}); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "Вы: распознанная фраза\n"; got != want {
		t.Fatalf("output=%q want=%q", got, want)
	}
}

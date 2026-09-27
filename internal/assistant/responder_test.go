package assistant

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/dialogue"
	"github.com/Seraf-seraf/voice_assistent/internal/llm"
)

type fakeGenerator struct {
	generate func(context.Context, llm.Request, llm.Emit) error
}

func (g fakeGenerator) Generate(ctx context.Context, request llm.Request, emit llm.Emit) error {
	return g.generate(ctx, request, emit)
}

type recordingResponseSink struct {
	pushes        []ResponseDelta
	completes     []Response
	aborts        []ResponseAbort
	abortContexts []context.Context
	pushFunc      func(context.Context, ResponseDelta) error
	completeFunc  func(context.Context, Response) error
	abortFunc     func(context.Context, ResponseAbort) error
	abortResult   ResponseAbortResult
}

func (s *recordingResponseSink) Push(ctx context.Context, delta ResponseDelta) error {
	s.pushes = append(s.pushes, delta)
	if s.pushFunc != nil {
		return s.pushFunc(ctx, delta)
	}
	return nil
}

func (s *recordingResponseSink) Complete(ctx context.Context, response Response) error {
	s.completes = append(s.completes, response)
	if s.completeFunc != nil {
		return s.completeFunc(ctx, response)
	}
	return nil
}

func (s *recordingResponseSink) Abort(ctx context.Context, abort ResponseAbort) (ResponseAbortResult, error) {
	s.aborts = append(s.aborts, abort)
	s.abortContexts = append(s.abortContexts, ctx)
	if s.abortFunc != nil {
		return s.abortResult, s.abortFunc(ctx, abort)
	}
	if s.abortResult != (ResponseAbortResult{}) {
		return s.abortResult, nil
	}
	return ResponseAbortResult{UtteranceID: abort.UtteranceID, TurnID: abort.TurnID}, nil
}

func newResponderWithComplete(t *testing.T, manager *dialogue.Manager, generator llm.Generator, options llm.Options, complete func(context.Context, Response) error) (*Responder, *recordingResponseSink) {
	t.Helper()
	sink := &recordingResponseSink{completeFunc: complete}
	responder, err := NewResponder(manager, generator, options, sink)
	if err != nil {
		t.Fatal(err)
	}
	return responder, sink
}

func newResponderTestManager(t *testing.T) *dialogue.Manager {
	t.Helper()
	manager, err := dialogue.New(dialogue.Options{
		SystemPrompt: "system prompt", ResponsePolicy: "response policy", MaxHistoryMessages: 12,
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func TestResponderPassesSnapshotOptionsAndCollectsTextDeltas(t *testing.T) {
	manager := newResponderTestManager(t)
	previousTurn, err := manager.BeginTurn("предыдущий вопрос")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.CompleteTurn(previousTurn, "предыдущий ответ"); err != nil {
		t.Fatal(err)
	}
	wantOptions := llm.Options{Temperature: 0.4, MaxTokens: 512}
	var request llm.Request
	insideGenerate := false
	var sink *recordingResponseSink
	generator := fakeGenerator{generate: func(_ context.Context, got llm.Request, emit llm.Emit) error {
		insideGenerate = true
		request = got
		for index, fragment := range []string{" При", "вет, ", "мир! "} {
			if err := emit(llm.TextDelta{Text: fragment}); err != nil {
				return err
			}
			if got, want := len(sink.pushes), index+1; got != want {
				t.Fatalf("sink pushes during Generate = %d, want %d", got, want)
			}
			if len(sink.completes) != 0 {
				t.Fatal("sink Complete called before Generate returned")
			}
		}
		insideGenerate = false
		return nil
	}}
	sink = &recordingResponseSink{completeFunc: func(_ context.Context, got Response) error {
		if insideGenerate {
			t.Fatal("sink Complete called during Generate")
		}
		return nil
	}}
	responder, err := NewResponder(manager, generator, wantOptions, sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := responder.Handle(context.Background(), Query{UtteranceID: 42, Text: "текущий вопрос", Canonical: "другой canonical"}); err != nil {
		t.Fatal(err)
	}
	wantMessages := []dialogue.Message{
		{Role: dialogue.RoleUser, Content: "предыдущий вопрос"},
		{Role: dialogue.RoleAssistant, Content: "предыдущий ответ"},
		{Role: dialogue.RoleUser, Content: "текущий вопрос"},
	}
	wantSnapshot := dialogue.Snapshot{SystemPrompt: "system prompt", ResponsePolicy: "response policy", Messages: wantMessages}
	if request.Dialogue.SystemPrompt != wantSnapshot.SystemPrompt || request.Dialogue.ResponsePolicy != wantSnapshot.ResponsePolicy || !reflect.DeepEqual(request.Dialogue.Messages, wantSnapshot.Messages) {
		t.Fatalf("request snapshot = %+v, want %+v", request.Dialogue, wantSnapshot)
	}
	if request.Options != wantOptions {
		t.Fatalf("request options = %+v, want %+v", request.Options, wantOptions)
	}
	wantResponse := Response{UtteranceID: 42, TurnID: 2, Text: "Привет, мир!"}
	if len(sink.completes) != 1 || sink.completes[0] != wantResponse {
		t.Fatalf("sink completions = %+v, want [%+v]", sink.completes, wantResponse)
	}
	if len(sink.aborts) != 0 {
		t.Fatalf("sink Abort calls = %d, want 0", len(sink.aborts))
	}
	wantHistory := append(wantMessages, dialogue.Message{Role: dialogue.RoleAssistant, Content: "Привет, мир!"})
	if !reflect.DeepEqual(manager.Snapshot().Messages, wantHistory) {
		t.Fatalf("history = %+v, want %+v", manager.Snapshot().Messages, wantHistory)
	}
}

func TestResponderStreamsTrimmedWhitespaceAndStoresSameText(t *testing.T) {
	manager := newResponderTestManager(t)
	generator := fakeGenerator{generate: func(_ context.Context, _ llm.Request, emit llm.Emit) error {
		for _, fragment := range []string{"  При", "вет", ", ", "мир! ", "  "} {
			if err := emit(llm.TextDelta{Text: fragment}); err != nil {
				return err
			}
		}
		return nil
	}}
	responder, sink := newResponderWithComplete(t, manager, generator, llm.Options{Temperature: 0.4, MaxTokens: 20}, nil)
	if err := responder.Handle(context.Background(), Query{UtteranceID: 42, Text: "вопрос"}); err != nil {
		t.Fatal(err)
	}
	var streamed strings.Builder
	for _, delta := range sink.pushes {
		_, _ = streamed.WriteString(delta.Text)
	}
	if got, want := streamed.String(), "Привет, мир!"; got != want {
		t.Fatalf("streamed text=%q want=%q", got, want)
	}
	wantResponse := Response{UtteranceID: 42, TurnID: 1, Text: "Привет, мир!"}
	if len(sink.completes) != 1 || sink.completes[0] != wantResponse {
		t.Fatalf("completes=%+v want=[%+v]", sink.completes, wantResponse)
	}
	wantHistory := []dialogue.Message{{Role: dialogue.RoleUser, Content: "вопрос"}, {Role: dialogue.RoleAssistant, Content: "Привет, мир!"}}
	if !reflect.DeepEqual(manager.Snapshot().Messages, wantHistory) {
		t.Fatalf("history=%+v want=%+v", manager.Snapshot().Messages, wantHistory)
	}
}

func TestResponderBuffersOnlyTrailingWhitespaceUntilNextText(t *testing.T) {
	manager := newResponderTestManager(t)
	var sink *recordingResponseSink
	generator := fakeGenerator{generate: func(_ context.Context, _ llm.Request, emit llm.Emit) error {
		for index, fragment := range []string{"текст", " \t", "между", "  "} {
			if err := emit(llm.TextDelta{Text: fragment}); err != nil {
				return err
			}
			if index == 1 && (len(sink.pushes) != 1 || sink.pushes[0].Text != "текст") {
				t.Fatalf("pending whitespace was emitted too early: %+v", sink.pushes)
			}
			if index == 2 && (len(sink.pushes) != 2 || sink.pushes[1].Text != " \tмежду") {
				t.Fatalf("internal whitespace not preserved: %+v", sink.pushes)
			}
		}
		return nil
	}}
	sink = &recordingResponseSink{}
	responder, err := NewResponder(manager, generator, llm.Options{Temperature: 0.4, MaxTokens: 20}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := responder.Handle(context.Background(), Query{Text: "вопрос"}); err != nil {
		t.Fatal(err)
	}
	var streamed strings.Builder
	for _, delta := range sink.pushes {
		_, _ = streamed.WriteString(delta.Text)
	}
	if got, want := streamed.String(), "текст \tмежду"; got != want {
		t.Fatalf("streamed=%q want=%q", got, want)
	}
}

func TestResponderRecordsOnlyConfirmedPrefixOnSpeechInterrupt(t *testing.T) {
	manager := newResponderTestManager(t)
	started := make(chan struct{})
	generator := fakeGenerator{generate: func(ctx context.Context, _ llm.Request, emit llm.Emit) error {
		if err := emit(llm.TextDelta{Text: "Первое предложение. Второе."}); err != nil {
			return err
		}
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}}
	sink := &recordingResponseSink{abortResult: ResponseAbortResult{UtteranceID: 7, TurnID: 1, PlayedText: "Первое предложение."}}
	responder, err := NewResponder(manager, generator, llm.Options{Temperature: 0.4, MaxTokens: 20}, sink)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	done := make(chan error, 1)
	go func() { done <- responder.Handle(ctx, Query{UtteranceID: 7, Text: "вопрос"}) }()
	<-started
	cancel(ErrSpeechInterrupted)
	if err := <-done; !errors.Is(err, ErrSpeechInterrupted) || !errors.Is(err, context.Canceled) {
		t.Fatalf("ошибка Handle()=%v, ожидались причина прерывания и отменённый контекст", err)
	}
	want := []dialogue.Message{{Role: dialogue.RoleUser, Content: "вопрос"}, {Role: dialogue.RoleAssistant, Content: "Первое предложение."}}
	if !reflect.DeepEqual(manager.Snapshot().Messages, want) {
		t.Fatalf("история=%+v, ожидалась %+v", manager.Snapshot().Messages, want)
	}
	if len(sink.aborts) != 1 || len(sink.completes) != 0 {
		t.Fatalf("отмен вывода=%d, завершений вывода=%d", len(sink.aborts), len(sink.completes))
	}
}

func TestResponderRejectsInvalidAbortProgressAndDoesNotRecordIt(t *testing.T) {
	for _, result := range []ResponseAbortResult{
		{UtteranceID: 99, TurnID: 1, PlayedText: "префикс"},
		{UtteranceID: 1, TurnID: 1, PlayedText: "\xff"},
		{UtteranceID: 1, TurnID: 1, PlayedText: "не префикс"},
	} {
		manager := newResponderTestManager(t)
		started := make(chan struct{})
		generator := fakeGenerator{generate: func(ctx context.Context, _ llm.Request, emit llm.Emit) error {
			close(started)
			if err := emit(llm.TextDelta{Text: "сгенерированный ответ"}); err != nil {
				return err
			}
			<-ctx.Done()
			return ctx.Err()
		}}
		sink := &recordingResponseSink{abortResult: result}
		responder, err := NewResponder(manager, generator, llm.Options{Temperature: 0.4, MaxTokens: 20}, sink)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancelCause(context.Background())
		done := make(chan error, 1)
		go func() { done <- responder.Handle(ctx, Query{UtteranceID: 1, Text: "вопрос"}) }()
		<-started
		cancel(ErrSpeechInterrupted)
		if err := <-done; !errors.Is(err, ErrInvalidResponseProgress) {
			t.Fatalf("ошибка Handle()=%v, ожидалась недостоверная квитанция", err)
		}
		if got := manager.Snapshot().Messages; len(got) != 1 || got[0].Role != dialogue.RoleUser {
			t.Fatalf("недостоверная квитанция изменила историю: %+v", got)
		}
	}
}

func TestResponderOrdinaryFailureAbortsTurnWithoutRecordingOutput(t *testing.T) {
	manager := newResponderTestManager(t)
	generateErr := errors.New("сбой генерации")
	responder, sink := newResponderWithComplete(t, manager, fakeGenerator{generate: func(_ context.Context, _ llm.Request, emit llm.Emit) error {
		if err := emit(llm.TextDelta{Text: "partial"}); err != nil {
			return err
		}
		return generateErr
	}}, llm.Options{Temperature: 0.4, MaxTokens: 20}, nil)
	if err := responder.Handle(context.Background(), Query{UtteranceID: 1, Text: "вопрос"}); !errors.Is(err, generateErr) {
		t.Fatalf("ошибка Handle()=%v, ожидалась ошибка генерации", err)
	}
	if got := manager.Snapshot().Messages; len(got) != 1 || got[0].Role != dialogue.RoleUser {
		t.Fatalf("обычная ошибка записала ответ ассистента: %+v", got)
	}
	if len(sink.aborts) != 1 || len(sink.completes) != 0 {
		t.Fatalf("отмен вывода=%d, завершений вывода=%d", len(sink.aborts), len(sink.completes))
	}
}

func TestResponderUsesTurnIDAndCompletesBeforeNextRequest(t *testing.T) {
	manager := newResponderTestManager(t)
	var requests []llm.Request
	generator := fakeGenerator{generate: func(_ context.Context, request llm.Request, emit llm.Emit) error {
		requests = append(requests, request)
		if len(requests) == 1 {
			if err := emit(llm.TextDelta{Text: "answer one"}); err != nil {
				return err
			}
			return nil
		}
		if err := emit(llm.TextDelta{Text: "answer two"}); err != nil {
			return err
		}
		return nil
	}}
	responder, sink := newResponderWithComplete(t, manager, generator, llm.Options{Temperature: 0.4, MaxTokens: 8}, func(_ context.Context, response Response) error {
		wantTurnID := uint64(len(requests))
		if response.TurnID != wantTurnID {
			t.Fatalf("response TurnID = %d, want %d", response.TurnID, wantTurnID)
		}
		if len(requests) == 1 && response.UtteranceID != 42 {
			t.Fatalf("response UtteranceID = %d, want 42", response.UtteranceID)
		}
		return nil
	})
	if err := responder.Handle(context.Background(), Query{UtteranceID: 42, Text: "first query"}); err != nil {
		t.Fatal(err)
	}
	if err := responder.Handle(context.Background(), Query{UtteranceID: 99, Text: "second query"}); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 || len(sink.completes) != 2 || len(sink.aborts) != 0 {
		t.Fatalf("requests=%d completes=%d aborts=%d", len(requests), len(sink.completes), len(sink.aborts))
	}
	wantSecondMessages := []dialogue.Message{
		{Role: dialogue.RoleUser, Content: "first query"},
		{Role: dialogue.RoleAssistant, Content: "answer one"},
		{Role: dialogue.RoleUser, Content: "second query"},
	}
	if !reflect.DeepEqual(requests[1].Dialogue.Messages, wantSecondMessages) {
		t.Fatalf("second request messages = %+v, want %+v", requests[1].Dialogue.Messages, wantSecondMessages)
	}
}

func TestResponderAbortsTurnAfterGenerationErrors(t *testing.T) {
	for _, afterDelta := range []bool{false, true} {
		name := "before delta"
		if afterDelta {
			name = "after delta"
		}
		t.Run(name, func(t *testing.T) {
			manager := newResponderTestManager(t)
			generationErr := errors.New("ошибка генерации")
			calls := 0
			generator := fakeGenerator{generate: func(_ context.Context, _ llm.Request, emit llm.Emit) error {
				calls++
				if calls == 1 {
					if afterDelta {
						if err := emit(llm.TextDelta{Text: "partial"}); err != nil {
							return err
						}
					}
					return generationErr
				}
				return emit(llm.TextDelta{Text: "complete"})
			}}
			responder, sink := newResponderWithComplete(t, manager, generator, llm.Options{Temperature: 0, MaxTokens: 1}, nil)
			if err := responder.Handle(context.Background(), Query{Text: "first query"}); !errors.Is(err, generationErr) {
				t.Fatalf("Handle() error = %v, want generation cause", err)
			}
			if len(sink.completes) != 0 || len(sink.aborts) != 1 {
				t.Fatalf("sink completes=%d aborts=%d after failed generation", len(sink.completes), len(sink.aborts))
			}
			wantAfterFailure := []dialogue.Message{{Role: dialogue.RoleUser, Content: "first query"}}
			if !reflect.DeepEqual(manager.Snapshot().Messages, wantAfterFailure) {
				t.Fatalf("history after failure = %+v, want user message only", manager.Snapshot().Messages)
			}
			if err := responder.Handle(context.Background(), Query{Text: "second query"}); err != nil {
				t.Fatalf("next Handle() = %v; prior turn was not released", err)
			}
		})
	}
}

func TestResponderRejectsEmptyGeneratedResponse(t *testing.T) {
	for _, test := range []struct {
		name      string
		fragments []string
	}{
		{name: "no fragments"},
		{name: "empty fragments", fragments: []string{"", ""}},
		{name: "whitespace", fragments: []string{" \t", "\n "}},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager := newResponderTestManager(t)
			generator := fakeGenerator{generate: func(_ context.Context, _ llm.Request, emit llm.Emit) error {
				for _, fragment := range test.fragments {
					if err := emit(llm.TextDelta{Text: fragment}); err != nil {
						return err
					}
				}
				return nil
			}}
			responder, sink := newResponderWithComplete(t, manager, generator, llm.Options{Temperature: 1, MaxTokens: 10}, nil)
			if err := responder.Handle(context.Background(), Query{Text: "query"}); !errors.Is(err, ErrEmptyResponse) {
				t.Fatalf("Handle() error = %v, want ErrEmptyResponse", err)
			}
			if len(sink.pushes) != 0 || len(sink.completes) != 0 || len(sink.aborts) != 1 || !reflect.DeepEqual(manager.Snapshot().Messages, []dialogue.Message{{Role: dialogue.RoleUser, Content: "query"}}) {
				t.Fatalf("pushes=%d completes=%d aborts=%d history=%+v", len(sink.pushes), len(sink.completes), len(sink.aborts), manager.Snapshot().Messages)
			}
			if _, err := manager.BeginTurn("next query"); err != nil {
				t.Fatalf("turn remains active after empty response: %v", err)
			}
		})
	}
}

func TestResponderAbortsWhenSinkCompleteFails(t *testing.T) {
	manager := newResponderTestManager(t)
	handlerErr := errors.New("приёмник отклонил ответ")
	generator := fakeGenerator{generate: func(_ context.Context, _ llm.Request, emit llm.Emit) error {
		return emit(llm.TextDelta{Text: "answer"})
	}}
	responder, sink := newResponderWithComplete(t, manager, generator, llm.Options{Temperature: 0, MaxTokens: 1}, func(context.Context, Response) error { return handlerErr })
	if err := responder.Handle(context.Background(), Query{Text: "query"}); !errors.Is(err, handlerErr) {
		t.Fatalf("Handle() error = %v, want handler cause", err)
	}
	if len(sink.completes) != 1 || len(sink.aborts) != 1 {
		t.Fatalf("sink completes=%d aborts=%d", len(sink.completes), len(sink.aborts))
	}
	if !reflect.DeepEqual(manager.Snapshot().Messages, []dialogue.Message{{Role: dialogue.RoleUser, Content: "query"}}) {
		t.Fatalf("history after rejected response = %+v", manager.Snapshot().Messages)
	}
	if _, err := manager.BeginTurn("next query"); err != nil {
		t.Fatalf("turn remains active after handler error: %v", err)
	}
}

func TestResponderAbortsWhenSinkPushFails(t *testing.T) {
	manager := newResponderTestManager(t)
	pushErr := errors.New("ошибка вывода")
	generator := fakeGenerator{generate: func(_ context.Context, _ llm.Request, emit llm.Emit) error {
		return emit(llm.TextDelta{Text: "answer"})
	}}
	sink := &recordingResponseSink{pushFunc: func(_ context.Context, _ ResponseDelta) error { return pushErr }}
	responder, err := NewResponder(manager, generator, llm.Options{Temperature: 0, MaxTokens: 1}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := responder.Handle(context.Background(), Query{Text: "query"}); !errors.Is(err, pushErr) {
		t.Fatalf("Handle() error = %v, want push cause", err)
	}
	if len(sink.pushes) != 1 || len(sink.completes) != 0 || len(sink.aborts) != 1 {
		t.Fatalf("sink pushes=%d completes=%d aborts=%d", len(sink.pushes), len(sink.completes), len(sink.aborts))
	}
	if !reflect.DeepEqual(manager.Snapshot().Messages, []dialogue.Message{{Role: dialogue.RoleUser, Content: "query"}}) {
		t.Fatalf("history after push failure = %+v", manager.Snapshot().Messages)
	}
}

func TestResponderPreservesGenerationAndSinkAbortErrors(t *testing.T) {
	manager := newResponderTestManager(t)
	generationErr := errors.New("ошибка генерации")
	abortErr := errors.New("ошибка очистки вывода")
	generator := fakeGenerator{generate: func(context.Context, llm.Request, llm.Emit) error { return generationErr }}
	sink := &recordingResponseSink{abortFunc: func(context.Context, ResponseAbort) error { return abortErr }}
	responder, err := NewResponder(manager, generator, llm.Options{Temperature: 0, MaxTokens: 1}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := responder.Handle(context.Background(), Query{UtteranceID: 42, Text: "query"}); !errors.Is(err, generationErr) || !errors.Is(err, abortErr) {
		t.Fatalf("Handle() error=%v, want generation and cleanup causes", err)
	}
	if len(sink.aborts) != 1 || sink.aborts[0] != (ResponseAbort{UtteranceID: 42, TurnID: 1}) {
		t.Fatalf("Abort calls=%+v", sink.aborts)
	}
	if _, err := manager.BeginTurn("next query"); err != nil {
		t.Fatalf("dialogue turn was not released after sink cleanup failure: %v", err)
	}
}

func TestResponderCancellationBoundaries(t *testing.T) {
	t.Run("already canceled", func(t *testing.T) {
		manager := newResponderTestManager(t)
		generatorCalls := 0
		generator := fakeGenerator{generate: func(context.Context, llm.Request, llm.Emit) error { generatorCalls++; return nil }}
		responder, sink := newResponderWithComplete(t, manager, generator, llm.Options{Temperature: 0, MaxTokens: 1}, nil)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := responder.Handle(ctx, Query{Text: "query"}); !errors.Is(err, context.Canceled) {
			t.Fatalf("Handle() error = %v", err)
		}
		if generatorCalls != 0 || len(sink.pushes)+len(sink.completes)+len(sink.aborts) != 0 || len(manager.Snapshot().Messages) != 0 {
			t.Fatalf("generator=%d sink=%+v history=%+v", generatorCalls, sink, manager.Snapshot().Messages)
		}
	})

	t.Run("during generate", func(t *testing.T) {
		manager := newResponderTestManager(t)
		started := make(chan struct{})
		generator := fakeGenerator{generate: func(ctx context.Context, _ llm.Request, emit llm.Emit) error {
			if err := emit(llm.TextDelta{Text: "visible"}); err != nil {
				return err
			}
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}}
		responder, sink := newResponderWithComplete(t, manager, generator, llm.Options{Temperature: 0, MaxTokens: 1}, nil)
		type contextKey string
		requestContext := context.WithValue(context.Background(), contextKey("trace"), "kept")
		ctx, cancel := context.WithCancel(requestContext)
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- responder.Handle(ctx, Query{Text: "query"}) }()
		awaitResponderSignal(t, started)
		cancel()
		if err := awaitResponderResult(t, done); !errors.Is(err, context.Canceled) {
			t.Fatalf("Handle() error = %v", err)
		}
		if len(sink.pushes) != 1 || len(sink.completes) != 0 || len(sink.aborts) != 1 || !reflect.DeepEqual(manager.Snapshot().Messages, []dialogue.Message{{Role: dialogue.RoleUser, Content: "query"}}) {
			t.Fatalf("sink=%+v history=%+v", sink, manager.Snapshot().Messages)
		}
		abortContext := sink.abortContexts[0]
		if err := abortContext.Err(); err != nil || abortContext.Value(contextKey("trace")) != "kept" {
			t.Fatalf("abort context err=%v trace=%v", err, abortContext.Value(contextKey("trace")))
		}
		if _, err := manager.BeginTurn("next query"); err != nil {
			t.Fatalf("turn remains active after cancellation: %v", err)
		}
	})

	t.Run("after generate before handler", func(t *testing.T) {
		manager := newResponderTestManager(t)
		ctx, cancel := context.WithCancel(context.Background())
		generator := fakeGenerator{generate: func(_ context.Context, _ llm.Request, emit llm.Emit) error {
			if err := emit(llm.TextDelta{Text: "visible"}); err != nil {
				return err
			}
			cancel()
			return nil
		}}
		responder, sink := newResponderWithComplete(t, manager, generator, llm.Options{Temperature: 0, MaxTokens: 1}, nil)
		if err := responder.Handle(ctx, Query{Text: "query"}); !errors.Is(err, context.Canceled) {
			t.Fatalf("Handle() error = %v", err)
		}
		if len(sink.pushes) != 1 || len(sink.completes) != 0 || len(sink.aborts) != 1 || !reflect.DeepEqual(manager.Snapshot().Messages, []dialogue.Message{{Role: dialogue.RoleUser, Content: "query"}}) {
			t.Fatalf("sink=%+v history=%+v", sink, manager.Snapshot().Messages)
		}
		if _, err := manager.BeginTurn("next query"); err != nil {
			t.Fatalf("turn remains active after post-generation cancellation: %v", err)
		}
	})

	t.Run("sink accepts and cancels", func(t *testing.T) {
		manager := newResponderTestManager(t)
		ctx, cancel := context.WithCancel(context.Background())
		generator := fakeGenerator{generate: func(_ context.Context, _ llm.Request, emit llm.Emit) error {
			return emit(llm.TextDelta{Text: "accepted"})
		}}
		responder, sink := newResponderWithComplete(t, manager, generator, llm.Options{Temperature: 0, MaxTokens: 1}, func(context.Context, Response) error { cancel(); return nil })
		if err := responder.Handle(ctx, Query{Text: "query"}); err != nil {
			t.Fatalf("Handle() after sink accepted response = %v", err)
		}
		if len(sink.completes) != 1 || len(sink.aborts) != 0 {
			t.Fatalf("sink completes=%d aborts=%d", len(sink.completes), len(sink.aborts))
		}
		want := []dialogue.Message{{Role: dialogue.RoleUser, Content: "query"}, {Role: dialogue.RoleAssistant, Content: "accepted"}}
		if !reflect.DeepEqual(manager.Snapshot().Messages, want) {
			t.Fatalf("history = %+v, want accepted response", manager.Snapshot().Messages)
		}
	})
}

func TestResponderDoesNotAbortAnotherActiveTurn(t *testing.T) {
	manager := newResponderTestManager(t)
	foreignTurn, err := manager.BeginTurn("other owner's query")
	if err != nil {
		t.Fatal(err)
	}
	generatorCalls := 0
	generator := fakeGenerator{generate: func(context.Context, llm.Request, llm.Emit) error { generatorCalls++; return nil }}
	sink := &recordingResponseSink{}
	responder, err := NewResponder(manager, generator, llm.Options{Temperature: 0, MaxTokens: 1}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := responder.Handle(context.Background(), Query{Text: "responder query"}); !errors.Is(err, dialogue.ErrTurnActive) {
		t.Fatalf("Handle() error = %v, want ErrTurnActive", err)
	}
	if generatorCalls != 0 || len(sink.aborts) != 0 || !reflect.DeepEqual(manager.Snapshot().Messages, []dialogue.Message{{Role: dialogue.RoleUser, Content: "other owner's query"}}) {
		t.Fatalf("generator calls=%d history=%+v", generatorCalls, manager.Snapshot().Messages)
	}
	if err := manager.CompleteTurn(foreignTurn, "other owner's response"); err != nil {
		t.Fatalf("foreign turn was altered: %v", err)
	}
}

func TestNewResponderRejectsNilDependenciesAndInvalidOptions(t *testing.T) {
	manager := newResponderTestManager(t)
	generatorCalls := 0
	validGenerator := fakeGenerator{generate: func(context.Context, llm.Request, llm.Emit) error { generatorCalls++; return nil }}
	var typedNilGenerator *fakeGenerator
	validSink := &recordingResponseSink{}
	var typedNilSink *recordingResponseSink
	validOptions := llm.Options{Temperature: 0.4, MaxTokens: 16}
	tests := []struct {
		name      string
		manager   *dialogue.Manager
		generator llm.Generator
		options   llm.Options
		sink      ResponseSink
	}{
		{name: "nil manager", manager: nil, generator: validGenerator, options: validOptions, sink: validSink},
		{name: "nil generator", manager: manager, generator: nil, options: validOptions, sink: validSink},
		{name: "typed nil generator", manager: manager, generator: typedNilGenerator, options: validOptions, sink: validSink},
		{name: "nil sink", manager: manager, generator: validGenerator, options: validOptions, sink: nil},
		{name: "typed nil sink", manager: manager, generator: validGenerator, options: validOptions, sink: typedNilSink},
		{name: "invalid options", manager: manager, generator: validGenerator, options: llm.Options{Temperature: 0, MaxTokens: 0}, sink: validSink},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewResponder(test.manager, test.generator, test.options, test.sink)
			if err == nil {
				t.Fatal("NewResponder accepted invalid dependency")
			}
			if test.name == "invalid options" && !errors.Is(err, llm.ErrInvalidOptions) {
				t.Fatalf("NewResponder() error = %v, want llm.ErrInvalidOptions", err)
			}
		})
	}
	if _, err := NewResponder(manager, validGenerator, validOptions, validSink); err != nil {
		t.Fatalf("NewResponder(valid dependencies) = %v", err)
	}
	if generatorCalls != 0 {
		t.Fatalf("constructor called generator %d times", generatorCalls)
	}
	if len(manager.Snapshot().Messages) != 0 {
		t.Fatalf("constructor mutated history: %+v", manager.Snapshot().Messages)
	}
}

func awaitResponderSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatal("Generator.Generate was not called")
	}
}

func awaitResponderResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("Responder.Handle did not finish")
		return nil
	}
}

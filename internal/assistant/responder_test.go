package assistant

import (
	"context"
	"errors"
	"reflect"
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
	responseCalls := 0
	insideGenerate := false
	generator := fakeGenerator{generate: func(_ context.Context, got llm.Request, emit llm.Emit) error {
		insideGenerate = true
		request = got
		for _, fragment := range []string{" При", "вет, ", "мир! "} {
			if err := emit(llm.TextDelta{Text: fragment}); err != nil {
				return err
			}
			if responseCalls != 0 {
				t.Fatal("ResponseHandler called before Generate returned")
			}
		}
		insideGenerate = false
		return nil
	}}
	var response Response
	responder, err := NewResponder(manager, generator, wantOptions, func(_ context.Context, got Response) error {
		if insideGenerate {
			t.Fatal("ResponseHandler called during Generate")
		}
		responseCalls++
		response = got
		return nil
	})
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
	if response != wantResponse {
		t.Fatalf("response = %+v, want %+v", response, wantResponse)
	}
	if responseCalls != 1 {
		t.Fatalf("ResponseHandler calls = %d, want 1", responseCalls)
	}
	wantHistory := append(wantMessages, dialogue.Message{Role: dialogue.RoleAssistant, Content: "Привет, мир!"})
	if !reflect.DeepEqual(manager.Snapshot().Messages, wantHistory) {
		t.Fatalf("history = %+v, want %+v", manager.Snapshot().Messages, wantHistory)
	}
}

func TestResponderUsesTurnIDAndCompletesBeforeNextRequest(t *testing.T) {
	manager := newResponderTestManager(t)
	var requests []llm.Request
	responseCalls := 0
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
	responder, err := NewResponder(manager, generator, llm.Options{Temperature: 0.4, MaxTokens: 8}, func(_ context.Context, response Response) error {
		responseCalls++
		wantTurnID := uint64(responseCalls)
		if response.TurnID != wantTurnID {
			t.Fatalf("response TurnID = %d, want %d", response.TurnID, wantTurnID)
		}
		if responseCalls == 1 && response.UtteranceID != 42 {
			t.Fatalf("response UtteranceID = %d, want 42", response.UtteranceID)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := responder.Handle(context.Background(), Query{UtteranceID: 42, Text: "first query"}); err != nil {
		t.Fatal(err)
	}
	if err := responder.Handle(context.Background(), Query{UtteranceID: 99, Text: "second query"}); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 || responseCalls != 2 {
		t.Fatalf("requests=%d responses=%d", len(requests), responseCalls)
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
			generationErr := errors.New("generation failed")
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
			responseCalls := 0
			responder, err := NewResponder(manager, generator, llm.Options{Temperature: 0, MaxTokens: 1}, func(context.Context, Response) error { responseCalls++; return nil })
			if err != nil {
				t.Fatal(err)
			}
			if err := responder.Handle(context.Background(), Query{Text: "first query"}); !errors.Is(err, generationErr) {
				t.Fatalf("Handle() error = %v, want generation cause", err)
			}
			if responseCalls != 0 {
				t.Fatal("ResponseHandler called for failed generation")
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
			responses := 0
			responder, err := NewResponder(manager, generator, llm.Options{Temperature: 1, MaxTokens: 10}, func(context.Context, Response) error { responses++; return nil })
			if err != nil {
				t.Fatal(err)
			}
			if err := responder.Handle(context.Background(), Query{Text: "query"}); !errors.Is(err, ErrEmptyResponse) {
				t.Fatalf("Handle() error = %v, want ErrEmptyResponse", err)
			}
			if responses != 0 || !reflect.DeepEqual(manager.Snapshot().Messages, []dialogue.Message{{Role: dialogue.RoleUser, Content: "query"}}) {
				t.Fatalf("responses=%d history=%+v", responses, manager.Snapshot().Messages)
			}
			if _, err := manager.BeginTurn("next query"); err != nil {
				t.Fatalf("turn remains active after empty response: %v", err)
			}
		})
	}
}

func TestResponderAbortsWhenResponseHandlerFails(t *testing.T) {
	manager := newResponderTestManager(t)
	handlerErr := errors.New("consumer rejected response")
	generator := fakeGenerator{generate: func(_ context.Context, _ llm.Request, emit llm.Emit) error {
		return emit(llm.TextDelta{Text: "answer"})
	}}
	responder, err := NewResponder(manager, generator, llm.Options{Temperature: 0, MaxTokens: 1}, func(context.Context, Response) error { return handlerErr })
	if err != nil {
		t.Fatal(err)
	}
	if err := responder.Handle(context.Background(), Query{Text: "query"}); !errors.Is(err, handlerErr) {
		t.Fatalf("Handle() error = %v, want handler cause", err)
	}
	if !reflect.DeepEqual(manager.Snapshot().Messages, []dialogue.Message{{Role: dialogue.RoleUser, Content: "query"}}) {
		t.Fatalf("history after rejected response = %+v", manager.Snapshot().Messages)
	}
	if _, err := manager.BeginTurn("next query"); err != nil {
		t.Fatalf("turn remains active after handler error: %v", err)
	}
}

func TestResponderCancellationBoundaries(t *testing.T) {
	t.Run("already canceled", func(t *testing.T) {
		manager := newResponderTestManager(t)
		generatorCalls, handlerCalls := 0, 0
		generator := fakeGenerator{generate: func(context.Context, llm.Request, llm.Emit) error { generatorCalls++; return nil }}
		responder, err := NewResponder(manager, generator, llm.Options{Temperature: 0, MaxTokens: 1}, func(context.Context, Response) error { handlerCalls++; return nil })
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := responder.Handle(ctx, Query{Text: "query"}); !errors.Is(err, context.Canceled) {
			t.Fatalf("Handle() error = %v", err)
		}
		if generatorCalls != 0 || handlerCalls != 0 || len(manager.Snapshot().Messages) != 0 {
			t.Fatalf("generator=%d handler=%d history=%+v", generatorCalls, handlerCalls, manager.Snapshot().Messages)
		}
	})

	t.Run("during generate", func(t *testing.T) {
		manager := newResponderTestManager(t)
		started := make(chan struct{})
		generator := fakeGenerator{generate: func(ctx context.Context, _ llm.Request, _ llm.Emit) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}}
		handlerCalls := 0
		responder, err := NewResponder(manager, generator, llm.Options{Temperature: 0, MaxTokens: 1}, func(context.Context, Response) error { handlerCalls++; return nil })
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- responder.Handle(ctx, Query{Text: "query"}) }()
		awaitResponderSignal(t, started)
		cancel()
		if err := awaitResponderResult(t, done); !errors.Is(err, context.Canceled) {
			t.Fatalf("Handle() error = %v", err)
		}
		if handlerCalls != 0 || !reflect.DeepEqual(manager.Snapshot().Messages, []dialogue.Message{{Role: dialogue.RoleUser, Content: "query"}}) {
			t.Fatalf("handler=%d history=%+v", handlerCalls, manager.Snapshot().Messages)
		}
		if _, err := manager.BeginTurn("next query"); err != nil {
			t.Fatalf("turn remains active after cancellation: %v", err)
		}
	})

	t.Run("after generate before handler", func(t *testing.T) {
		manager := newResponderTestManager(t)
		ctx, cancel := context.WithCancel(context.Background())
		generator := fakeGenerator{generate: func(context.Context, llm.Request, llm.Emit) error { cancel(); return nil }}
		handlerCalls := 0
		responder, err := NewResponder(manager, generator, llm.Options{Temperature: 0, MaxTokens: 1}, func(context.Context, Response) error { handlerCalls++; return nil })
		if err != nil {
			t.Fatal(err)
		}
		if err := responder.Handle(ctx, Query{Text: "query"}); !errors.Is(err, context.Canceled) {
			t.Fatalf("Handle() error = %v", err)
		}
		if handlerCalls != 0 || !reflect.DeepEqual(manager.Snapshot().Messages, []dialogue.Message{{Role: dialogue.RoleUser, Content: "query"}}) {
			t.Fatalf("handler=%d history=%+v", handlerCalls, manager.Snapshot().Messages)
		}
		if _, err := manager.BeginTurn("next query"); err != nil {
			t.Fatalf("turn remains active after post-generation cancellation: %v", err)
		}
	})

	t.Run("handler accepts and cancels", func(t *testing.T) {
		manager := newResponderTestManager(t)
		ctx, cancel := context.WithCancel(context.Background())
		generator := fakeGenerator{generate: func(_ context.Context, _ llm.Request, emit llm.Emit) error {
			return emit(llm.TextDelta{Text: "accepted"})
		}}
		responder, err := NewResponder(manager, generator, llm.Options{Temperature: 0, MaxTokens: 1}, func(context.Context, Response) error { cancel(); return nil })
		if err != nil {
			t.Fatal(err)
		}
		if err := responder.Handle(ctx, Query{Text: "query"}); err != nil {
			t.Fatalf("Handle() after handler accepted response = %v", err)
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
	responder, err := NewResponder(manager, generator, llm.Options{Temperature: 0, MaxTokens: 1}, func(context.Context, Response) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := responder.Handle(context.Background(), Query{Text: "responder query"}); !errors.Is(err, dialogue.ErrTurnActive) {
		t.Fatalf("Handle() error = %v, want ErrTurnActive", err)
	}
	if generatorCalls != 0 || !reflect.DeepEqual(manager.Snapshot().Messages, []dialogue.Message{{Role: dialogue.RoleUser, Content: "other owner's query"}}) {
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
	handler := ResponseHandler(func(context.Context, Response) error { return nil })
	validOptions := llm.Options{Temperature: 0.4, MaxTokens: 16}
	tests := []struct {
		name      string
		manager   *dialogue.Manager
		generator llm.Generator
		options   llm.Options
		handler   ResponseHandler
	}{
		{name: "nil manager", manager: nil, generator: validGenerator, options: validOptions, handler: handler},
		{name: "nil generator", manager: manager, generator: nil, options: validOptions, handler: handler},
		{name: "typed nil generator", manager: manager, generator: typedNilGenerator, options: validOptions, handler: handler},
		{name: "nil handler", manager: manager, generator: validGenerator, options: validOptions, handler: nil},
		{name: "invalid options", manager: manager, generator: validGenerator, options: llm.Options{Temperature: 0, MaxTokens: 0}, handler: handler},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewResponder(test.manager, test.generator, test.options, test.handler)
			if err == nil {
				t.Fatal("NewResponder accepted invalid dependency")
			}
			if test.name == "invalid options" && !errors.Is(err, llm.ErrInvalidOptions) {
				t.Fatalf("NewResponder() error = %v, want llm.ErrInvalidOptions", err)
			}
		})
	}
	if _, err := NewResponder(manager, validGenerator, validOptions, handler); err != nil {
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

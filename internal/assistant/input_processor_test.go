package assistant

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/control/router"
	"github.com/Seraf-seraf/voice_assistent/internal/dialogue"
	"github.com/Seraf-seraf/voice_assistent/internal/transcript"
)

func inputProcessorDependencies(t *testing.T, mode router.Mode, wakeWindow time.Duration) (*transcript.Normalizer, *router.Router, *dialogue.Manager) {
	t.Helper()
	normalizer, err := transcript.NewNormalizer(transcript.Options{MinSignificantRunes: 2})
	if err != nil {
		t.Fatal(err)
	}
	controlRouter, err := router.New(router.Options{Mode: mode, WakePhrases: []string{"ассистент"}, WakeWindow: wakeWindow})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := dialogue.New(dialogue.Options{SystemPrompt: "system", ResponsePolicy: "policy", MaxHistoryMessages: 4})
	if err != nil {
		t.Fatal(err)
	}
	return normalizer, controlRouter, manager
}

func TestInputProcessorNormalizesAndPassesQueryMetadata(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	var got Query
	normalizer, controlRouter, manager := inputProcessorDependencies(t, router.ModeAlways, 0)
	processor, err := NewInputProcessor(normalizer, controlRouter, manager, func(_ context.Context, query Query) error { got = query; return nil }, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if err := processor.Handle(context.Background(), Transcription{UtteranceID: 42, Text: "  Привет,   МИР!  "}); err != nil {
		t.Fatal(err)
	}
	want := Query{UtteranceID: 42, Text: "Привет, МИР!", Canonical: "привет, мир"}
	if got != want {
		t.Fatalf("query = %+v, want %+v", got, want)
	}
	if len(manager.Snapshot().Messages) != 0 {
		t.Fatal("query started a dialogue turn")
	}
}

func TestInputProcessorIgnoresExpectedNormalizationErrorsAndIgnoredRoutes(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{name: "empty", text: " \t"},
		{name: "too short", text: "а"},
		{name: "filtered", text: "шум"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := transcript.Options{MinSignificantRunes: 2}
			if test.name == "filtered" {
				options.IgnoredExact = []string{"шум"}
			}
			normalizer, err := transcript.NewNormalizer(options)
			if err != nil {
				t.Fatal(err)
			}
			r, err := router.New(router.Options{Mode: router.ModeWake, WakePhrases: []string{"ассистент"}, WakeWindow: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			manager, err := dialogue.New(dialogue.Options{SystemPrompt: "system", ResponsePolicy: "policy", MaxHistoryMessages: 4})
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			processor, err := NewInputProcessor(normalizer, r, manager, func(context.Context, Query) error { calls++; return nil }, func() time.Time { return time.Now() })
			if err != nil {
				t.Fatal(err)
			}
			if err := processor.Handle(context.Background(), Transcription{Text: test.text}); err != nil {
				t.Fatalf("Handle() error = %v", err)
			}
			if calls != 0 {
				t.Fatalf("query handler called %d times", calls)
			}
		})
	}
}

func TestInputProcessorWakeWindowAndCommands(t *testing.T) {
	now := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)
	normalizer, controlRouter, manager := inputProcessorDependencies(t, router.ModeWake, time.Second)
	calls := 0
	var got Query
	processor, err := NewInputProcessor(normalizer, controlRouter, manager, func(_ context.Context, query Query) error { calls++; got = query; return nil }, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if err := processor.Handle(context.Background(), Transcription{Text: "обычная фраза без активации"}); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("ignored route became query")
	}
	if err := processor.Handle(context.Background(), Transcription{UtteranceID: 1, Text: "ассистент"}); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("wake phrase alone became query")
	}
	now = now.Add(500 * time.Millisecond)
	if err := processor.Handle(context.Background(), Transcription{UtteranceID: 2, Text: "Открой окно"}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || got != (Query{UtteranceID: 2, Text: "Открой окно", Canonical: "открой окно"}) {
		t.Fatalf("calls=%d query=%+v", calls, got)
	}

	turnID, err := manager.BeginTurn("предыдущий вопрос")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.CompleteTurn(turnID, "предыдущий ответ"); err != nil {
		t.Fatal(err)
	}
	if err := processor.Handle(context.Background(), Transcription{UtteranceID: 3, Text: "очисти историю"}); err != nil {
		t.Fatal(err)
	}
	if len(manager.Snapshot().Messages) != 0 {
		t.Fatal("reset history did not clear dialogue")
	}
	if calls != 1 {
		t.Fatalf("command was dispatched as query, calls=%d", calls)
	}
	if err := processor.Handle(context.Background(), Transcription{UtteranceID: 4, Text: "ассистент"}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("wake-only phrase became query")
	}
	if err := processor.Handle(context.Background(), Transcription{UtteranceID: 5, Text: "стоп"}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("stop command was dispatched as query")
	}
	if err := processor.Handle(context.Background(), Transcription{Text: "ассистент"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	if err := processor.Handle(context.Background(), Transcription{Text: "просроченная фраза"}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("query outside injected wake window was dispatched")
	}
}

func TestInputProcessorQueryHandlerErrorIsWrapped(t *testing.T) {
	wantErr := errors.New("query consumer failed")
	normalizer, controlRouter, manager := inputProcessorDependencies(t, router.ModeAlways, 0)
	processor, err := NewInputProcessor(normalizer, controlRouter, manager, func(context.Context, Query) error { return wantErr }, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := processor.Handle(context.Background(), Transcription{Text: "обычный вопрос"}); !errors.Is(err, wantErr) {
		t.Fatalf("Handle() error = %v", err)
	}
}

func TestInputProcessorPauseAndResumeUseRouterState(t *testing.T) {
	normalizer, controlRouter, manager := inputProcessorDependencies(t, router.ModeAlways, 0)
	calls := 0
	processor, err := NewInputProcessor(normalizer, controlRouter, manager, func(context.Context, Query) error { calls++; return nil }, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"пауза", "обычный запрос"} {
		if err := processor.Handle(context.Background(), Transcription{Text: text}); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 0 {
		t.Fatal("router pause did not suppress query")
	}
	if err := processor.Handle(context.Background(), Transcription{Text: "продолжай"}); err != nil {
		t.Fatal(err)
	}
	if err := processor.Handle(context.Background(), Transcription{Text: "обычный запрос"}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("query calls after resume = %d, want 1", calls)
	}
}

func TestNewInputProcessorRejectsNilDependencies(t *testing.T) {
	normalizer, controlRouter, manager := inputProcessorDependencies(t, router.ModeAlways, 0)
	handler := QueryHandler(func(context.Context, Query) error { return nil })
	clock := Clock(time.Now)
	tests := []struct {
		normalizer *transcript.Normalizer
		router     *router.Router
		manager    *dialogue.Manager
		handler    QueryHandler
		clock      Clock
	}{
		{nil, controlRouter, manager, handler, clock},
		{normalizer, nil, manager, handler, clock},
		{normalizer, controlRouter, nil, handler, clock},
		{normalizer, controlRouter, manager, nil, clock},
		{normalizer, controlRouter, manager, handler, nil},
	}
	for _, test := range tests {
		if _, err := NewInputProcessor(test.normalizer, test.router, test.manager, test.handler, test.clock); err == nil {
			t.Fatal("NewInputProcessor accepted nil dependency")
		}
	}
}

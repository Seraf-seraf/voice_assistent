//go:build llm_integration

package llamacpp_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Seraf-seraf/voice_assistent/internal/assistant"
	"github.com/Seraf-seraf/voice_assistent/internal/dialogue"
	"github.com/Seraf-seraf/voice_assistent/internal/llm"
	"github.com/Seraf-seraf/voice_assistent/internal/llm/llamacpp"
)

const publishedModelSHA256 = "f5b14da98939b60bbe1019a964eba656407e1e0b64f1fe3003ff6d650e93bfec"

func TestNativeModelLifecycle(t *testing.T) {
	modelPath := os.Getenv("ASSISTANT_LLM_MODEL")
	libraryDir := os.Getenv("ASSISTANT_LLM_LIBRARY_DIR")
	if modelPath == "" || libraryDir == "" {
		t.Fatal("ASSISTANT_LLM_MODEL and ASSISTANT_LLM_LIBRARY_DIR must be set for llm_integration")
	}
	before := modelHash(t, modelPath)
	t.Logf("GGUF SHA-256: %s (published reference: %s)", before, publishedModelSHA256)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	generator, err := llamacpp.Open(ctx, llamacpp.Options{
		ModelPath: modelPath, LibraryDir: libraryDir, ContextSize: 4096,
		GPULayers: 99, Threads: 4, Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("open native Qwen model: %v", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = generator.Close()
		}
	}()

	options := llm.Options{Temperature: 0, MaxTokens: 128}
	snapshot := dialogue.Snapshot{SystemPrompt: "Отвечай кратко.", ResponsePolicy: "Не используй внутренние рассуждения.", Messages: []dialogue.Message{{Role: dialogue.RoleUser, Content: "Коротко поздоровайся по-русски."}}}
	first := generateText(t, ctx, generator, llm.Request{Dialogue: snapshot, Options: options})
	if strings.TrimSpace(first) == "" || !utf8.ValidString(first) || strings.Contains(first, "<think>") || strings.Contains(first, "</think>") || strings.Contains(first, "<|") {
		t.Fatalf("invalid model response: %q", first)
	}

	sentinel := errors.New("остановиться после первой дельты")
	emitCtx, emitCancel := context.WithTimeout(ctx, time.Minute)
	var emitted int
	err = generator.Generate(emitCtx, llm.Request{Dialogue: snapshot, Options: llm.Options{Temperature: 0, MaxTokens: 512}}, func(llm.TextDelta) error {
		emitted++
		return sentinel
	})
	emitCancel()
	if !errors.Is(err, sentinel) || emitted != 1 {
		t.Fatalf("emit cancellation error=%v emitted=%d", err, emitted)
	}
	_ = generateText(t, ctx, generator, llm.Request{Dialogue: snapshot, Options: options})

	cancelCtx, cancelRequest := context.WithCancel(ctx)
	var cancelErr error
	var cancelEmits int
	cancelErr = generator.Generate(cancelCtx, llm.Request{Dialogue: snapshot, Options: llm.Options{Temperature: 0, MaxTokens: 512}}, func(delta llm.TextDelta) error {
		cancelEmits++
		cancelRequest()
		return cancelCtx.Err()
	})
	if !errors.Is(cancelErr, context.Canceled) || cancelEmits == 0 {
		t.Fatalf("context cancellation error=%v emits=%d", cancelErr, cancelEmits)
	}
	_ = generateText(t, ctx, generator, llm.Request{Dialogue: snapshot, Options: options})

	var emittedForLimit int
	err = generator.Generate(ctx, llm.Request{Dialogue: snapshot, Options: llm.Options{Temperature: 0, MaxTokens: 4097}}, func(llm.TextDelta) error { emittedForLimit++; return nil })
	if !errors.Is(err, llamacpp.ErrContextLimit) || emittedForLimit != 0 {
		t.Fatalf("token limit error=%v emitted=%d", err, emittedForLimit)
	}
	large := snapshot
	large.Messages = []dialogue.Message{{Role: dialogue.RoleUser, Content: strings.Repeat("слово ", 10000)}}
	if err := generator.Generate(ctx, llm.Request{Dialogue: large, Options: options}, func(llm.TextDelta) error { emittedForLimit++; return nil }); !errors.Is(err, llamacpp.ErrContextLimit) || emittedForLimit != 0 {
		t.Fatalf("prompt context limit error=%v emitted=%d", err, emittedForLimit)
	}

	requestA := llm.Request{Dialogue: snapshot, Options: options}
	requestB := llm.Request{Dialogue: dialogue.Snapshot{SystemPrompt: snapshot.SystemPrompt, ResponsePolicy: snapshot.ResponsePolicy, Messages: []dialogue.Message{{Role: dialogue.RoleUser, Content: "Назови любое другое слово."}}}, Options: options}
	answerA1 := generateText(t, ctx, generator, requestA)
	_ = generateText(t, ctx, generator, requestB)
	answerA2 := generateText(t, ctx, generator, requestA)
	if answerA1 != answerA2 {
		t.Fatalf("stateless response changed across independent request: first=%q second=%q", answerA1, answerA2)
	}

	manager, err := dialogue.New(dialogue.Options{SystemPrompt: snapshot.SystemPrompt, ResponsePolicy: snapshot.ResponsePolicy, MaxHistoryMessages: 20})
	if err != nil {
		t.Fatal(err)
	}
	accepted := &integrationResponseSink{}
	responder, err := assistant.NewResponder(manager, generator, options, accepted)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []assistant.Query{{UtteranceID: 1, Text: "Поздоровайся."}, {UtteranceID: 2, Text: "Скажи одно слово."}} {
		if err := responder.Handle(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	if len(accepted.responses) != 2 || accepted.aborts != 0 || len(manager.Snapshot().Messages) != 4 || manager.Snapshot().Messages[3].Role != dialogue.RoleAssistant {
		t.Fatalf("accepted=%d aborts=%d history=%+v", len(accepted.responses), accepted.aborts, manager.Snapshot().Messages)
	}

	if err := generator.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	if err := generator.Close(); err != nil {
		t.Fatalf("second Close(): %v", err)
	}
	if err := generator.Generate(ctx, requestA, func(llm.TextDelta) error { return nil }); !errors.Is(err, llamacpp.ErrClosed) {
		t.Fatalf("Generate after Close()=%v", err)
	}
	if after := modelHash(t, modelPath); after != before {
		t.Fatalf("model changed during test: before=%s after=%s", before, after)
	}
}

type integrationResponseSink struct {
	responses []assistant.Response
	current   strings.Builder
	aborts    int
}

func (s *integrationResponseSink) Push(_ context.Context, delta assistant.ResponseDelta) error {
	_, err := s.current.WriteString(delta.Text)
	return err
}

func (s *integrationResponseSink) Complete(_ context.Context, response assistant.Response) error {
	if got := s.current.String(); got != response.Text {
		return fmt.Errorf("потоковый ответ не совпадает с полным ответом")
	}
	s.responses = append(s.responses, response)
	s.current.Reset()
	return nil
}

func (s *integrationResponseSink) Abort(context.Context, assistant.ResponseAbort) (assistant.ResponseAbortResult, error) {
	s.aborts++
	return assistant.ResponseAbortResult{}, errors.New("неожиданная отмена вывода ответа")
}

func generateText(t *testing.T, ctx context.Context, generator llm.Generator, request llm.Request) string {
	t.Helper()
	var text strings.Builder
	if err := generator.Generate(ctx, request, func(delta llm.TextDelta) error {
		if !utf8.ValidString(delta.Text) {
			return fmt.Errorf("дельта содержит некорректный UTF-8")
		}
		_, err := io.WriteString(&text, delta.Text)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return text.String()
}

func modelHash(t *testing.T, path string) string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

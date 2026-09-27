package assistant

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/audio"
	"github.com/Seraf-seraf/voice_assistent/internal/dialogue"
	"github.com/Seraf-seraf/voice_assistent/internal/llm"
	"github.com/Seraf-seraf/voice_assistent/internal/tts"
)

type speechTestSynth struct {
	texts         chan string
	started       chan struct{}
	waitForCancel bool
	calls         atomic.Int32
	err           error
}

func (s *speechTestSynth) Synthesize(ctx context.Context, text string) (audio.PCM, error) {
	if err := ctx.Err(); err != nil {
		return audio.PCM{}, err
	}
	if s.started != nil {
		close(s.started)
	}
	if s.waitForCancel {
		<-ctx.Done()
	}
	if s.err != nil {
		return audio.PCM{}, s.err
	}
	if err := ctx.Err(); err != nil {
		return audio.PCM{}, err
	}
	index := s.calls.Add(1)
	s.texts <- text
	return audio.PCM{Samples: []float32{float32(index) / 10}, SampleRate: 22050}, nil
}

type speechTestPlayer struct {
	started        chan []float32
	gate           <-chan struct{}
	cancelObserved chan struct{}
	ignoreCancel   bool
	calls          atomic.Int32
	err            error
}

type interruptSpeechPlayer struct {
	calls   atomic.Int32
	started chan int32
}

func (p *interruptSpeechPlayer) Play(ctx context.Context, _ audio.PCM) error {
	call := p.calls.Add(1)
	p.started <- call
	if call == 2 {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func (p *speechTestPlayer) Play(ctx context.Context, pcm audio.PCM) error {
	p.calls.Add(1)
	p.started <- append([]float32(nil), pcm.Samples...)
	if p.gate != nil && p.calls.Load() == 1 {
		if p.ignoreCancel {
			<-ctx.Done()
			if p.cancelObserved != nil {
				close(p.cancelObserved)
			}
			<-p.gate
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.gate:
		}
	}
	return p.err
}

func TestSpokenResponseSinkStreamsAndCompletesAfterPlayback(t *testing.T) {
	manager, err := dialogue.New(dialogue.Options{SystemPrompt: "system", ResponsePolicy: "policy", MaxHistoryMessages: 10})
	if err != nil {
		t.Fatal(err)
	}
	continueLLM := make(chan struct{})
	llmReturned := make(chan struct{})
	generateErr := make(chan error, 1)
	generator := fakeGenerator{generate: func(ctx context.Context, _ llm.Request, emit llm.Emit) error {
		if err := emit(llm.TextDelta{Text: "Привет. Как"}); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-continueLLM:
		}
		if err := emit(llm.TextDelta{Text: " дела?"}); err != nil {
			return err
		}
		close(llmReturned)
		return nil
	}}
	textSink := &recordingResponseSink{}
	synth := &speechTestSynth{texts: make(chan string, 4)}
	playGate := make(chan struct{})
	player := &speechTestPlayer{started: make(chan []float32, 4), gate: playGate}
	spoken, err := NewSpokenResponseSink(textSink, synth, player, SpeechOptions{Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	responder, err := NewResponder(manager, generator, llm.Options{Temperature: 0.4, MaxTokens: 20}, spoken)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		generateErr <- responder.Handle(context.Background(), Query{UtteranceID: 71, Text: "вопрос"})
	}()
	if got := <-player.started; len(got) != 1 {
		t.Fatalf("first Play samples = %v", got)
	}
	select {
	case <-llmReturned:
		t.Fatal("LLM returned before test released it")
	default:
	}
	if got := <-synth.texts; got != "Привет." {
		t.Fatalf("first synthesized phrase = %q", got)
	}
	close(continueLLM)
	<-llmReturned
	if len(textSink.completes) != 0 {
		t.Fatal("text sink completed before the last playback drained")
	}
	close(playGate)
	select {
	case <-player.started:
	case <-time.After(time.Second):
		t.Fatal("second Play did not start after the first playback returned")
	}
	if err := <-generateErr; err != nil {
		t.Fatalf("Responder.Handle() error = %v", err)
	}
	if got := <-synth.texts; got != "Как дела?" {
		t.Fatalf("second synthesized phrase = %q", got)
	}
	if len(textSink.completes) != 1 || textSink.completes[0].Text != "Привет. Как дела?" {
		t.Fatalf("text completes = %+v", textSink.completes)
	}
	if synth.calls.Load() != 2 || player.calls.Load() != 2 {
		t.Fatalf("synthesis calls=%d playback calls=%d, want one per phrase", synth.calls.Load(), player.calls.Load())
	}
	messages := manager.Snapshot().Messages
	if len(messages) != 2 || messages[1].Role != dialogue.RoleAssistant || messages[1].Content != "Привет. Как дела?" {
		t.Fatalf("dialogue messages = %+v", messages)
	}
}

func TestNewSpokenResponseSinkRequiresValidDependenciesAndTimeout(t *testing.T) {
	var nilSynth *speechTestSynth
	var nilTextSink *recordingResponseSink
	var nilPlayer *speechTestPlayer
	validTextSink := &recordingResponseSink{}
	validPlayer := &speechTestPlayer{started: make(chan []float32, 1)}
	for _, test := range []struct {
		name   string
		text   ResponseSink
		synth  tts.Synthesizer
		player interface {
			Play(context.Context, audio.PCM) error
		}
		options SpeechOptions
	}{
		{name: "nil sink", synth: &speechTestSynth{texts: make(chan string, 1)}, player: validPlayer, options: SpeechOptions{Timeout: time.Second}},
		{name: "typed nil sink", text: nilTextSink, synth: &speechTestSynth{texts: make(chan string, 1)}, player: validPlayer, options: SpeechOptions{Timeout: time.Second}},
		{name: "typed nil synth", text: validTextSink, synth: nilSynth, player: validPlayer, options: SpeechOptions{Timeout: time.Second}},
		{name: "typed nil player", text: validTextSink, synth: &speechTestSynth{texts: make(chan string, 1)}, player: nilPlayer, options: SpeechOptions{Timeout: time.Second}},
		{name: "zero timeout", text: validTextSink, synth: &speechTestSynth{texts: make(chan string, 1)}, player: validPlayer},
	} {
		t.Run(test.name, func(t *testing.T) {
			if sink, err := NewSpokenResponseSink(test.text, test.synth, test.player, test.options); err == nil || sink != nil {
				t.Fatalf("NewSpokenResponseSink() = %v, %v; want error", sink, err)
			}
		})
	}
	if (SpeechOptions{}).Validate() == nil {
		t.Fatal("SpeechOptions.Validate() accepted zero timeout")
	}
}

func TestSpokenResponseSinkEnforcesRuneBudgetBeforeForwardingOverflow(t *testing.T) {
	for _, count := range []int{8191, 8192} {
		name := "8191 runes"
		if count == 8192 {
			name = "8192 runes"
		}
		t.Run(name, func(t *testing.T) {
			text := strings.Repeat("я", count)
			textSink := &recordingResponseSink{}
			synth := &speechTestSynth{texts: make(chan string, MaxSpeechResponseRunes)}
			player := &speechTestPlayer{started: make(chan []float32, MaxSpeechResponseRunes)}
			spoken, err := NewSpokenResponseSink(textSink, synth, player, SpeechOptions{Timeout: time.Minute})
			if err != nil {
				t.Fatal(err)
			}
			response := Response{UtteranceID: 1, TurnID: 2, Text: text}
			if err := spoken.Push(context.Background(), ResponseDelta{UtteranceID: 1, TurnID: 2, Text: text}); err != nil {
				t.Fatal(err)
			}
			if err := spoken.Complete(context.Background(), response); err != nil {
				t.Fatal(err)
			}
			wantCalls := int32((count + tts.MaxTextRunes - 1) / tts.MaxTextRunes)
			if synth.calls.Load() != wantCalls || len(textSink.pushes) != 1 {
				t.Fatalf("synth calls=%d want=%d text pushes=%d", synth.calls.Load(), wantCalls, len(textSink.pushes))
			}
		})
	}

	textSink := &recordingResponseSink{}
	synth := &speechTestSynth{texts: make(chan string, MaxSpeechResponseRunes)}
	player := &speechTestPlayer{started: make(chan []float32, MaxSpeechResponseRunes)}
	spoken, err := NewSpokenResponseSink(textSink, synth, player, SpeechOptions{Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	tooLong := strings.Repeat("я", MaxSpeechResponseRunes+1)
	if err := spoken.Push(context.Background(), ResponseDelta{UtteranceID: 3, TurnID: 4, Text: tooLong}); !errors.Is(err, ErrSpeechTextLimit) {
		t.Fatalf("oversized single Push() error = %v", err)
	}
	if len(textSink.pushes) != 0 || synth.calls.Load() != 0 {
		t.Fatalf("oversized Push forwarded text=%d synth calls=%d", len(textSink.pushes), synth.calls.Load())
	}
	accepted := strings.Repeat("я", MaxSpeechResponseRunes)
	if err := spoken.Push(context.Background(), ResponseDelta{UtteranceID: 5, TurnID: 6, Text: accepted}); err != nil {
		t.Fatal(err)
	}
	if err := spoken.Push(context.Background(), ResponseDelta{UtteranceID: 5, TurnID: 6, Text: "я"}); !errors.Is(err, ErrSpeechTextLimit) {
		t.Fatalf("cumulative overflow Push() error = %v", err)
	}
	if len(textSink.pushes) != 1 {
		t.Fatalf("console received %d pushes after overflow", len(textSink.pushes))
	}
	if _, err := spoken.Abort(context.Background(), ResponseAbort{UtteranceID: 5, TurnID: 6}); err != nil {
		t.Fatal(err)
	}
}

func TestSpokenResponseSinkRejectsMismatchAndAbortDropsTail(t *testing.T) {
	textSink := &recordingResponseSink{}
	synth := &speechTestSynth{texts: make(chan string, 4)}
	player := &speechTestPlayer{started: make(chan []float32, 4)}
	spoken, err := NewSpokenResponseSink(textSink, synth, player, SpeechOptions{Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if err := spoken.Push(context.Background(), ResponseDelta{UtteranceID: 7, TurnID: 8, Text: "хвост"}); err != nil {
		t.Fatal(err)
	}
	if err := spoken.Complete(context.Background(), Response{UtteranceID: 7, TurnID: 8, Text: "другой текст"}); !errors.Is(err, ErrSpeechTextMismatch) {
		t.Fatalf("mismatched Complete() error = %v", err)
	}
	if _, err := spoken.Abort(context.Background(), ResponseAbort{UtteranceID: 70, TurnID: 80}); !errors.Is(err, ErrSpeechState) {
		t.Fatalf("foreign Abort() error = %v", err)
	}
	if _, err := spoken.Abort(context.Background(), ResponseAbort{UtteranceID: 7, TurnID: 8}); err != nil {
		t.Fatal(err)
	}
	if synth.calls.Load() != 0 || len(textSink.aborts) != 1 {
		t.Fatalf("synth calls=%d text aborts=%d", synth.calls.Load(), len(textSink.aborts))
	}
	if _, err := spoken.Abort(context.Background(), ResponseAbort{UtteranceID: 7, TurnID: 8}); err != nil {
		t.Fatalf("repeated Abort() error = %v", err)
	}
	if err := spoken.Push(context.Background(), ResponseDelta{UtteranceID: 9, TurnID: 10, Text: "Новый ответ.\n"}); err != nil {
		t.Fatal(err)
	}
	if err := spoken.Complete(context.Background(), Response{UtteranceID: 9, TurnID: 10, Text: "Новый ответ.\n"}); err != nil {
		t.Fatal(err)
	}
	if got := <-synth.texts; got != "Новый ответ." {
		t.Fatalf("new session synthesized stale or unexpected text %q", got)
	}
}

func TestSpokenResponseSinkAbortWaitsForCurrentPlaybackAndKeepsErrors(t *testing.T) {
	cleanupErr := errors.New("ошибка очистки")
	textSink := &recordingResponseSink{abortFunc: func(context.Context, ResponseAbort) error {
		return cleanupErr
	}}
	synth := &speechTestSynth{texts: make(chan string, 2)}
	playGate := make(chan struct{})
	player := &speechTestPlayer{
		started: make(chan []float32, 1), gate: playGate, ignoreCancel: true,
		cancelObserved: make(chan struct{}),
	}
	spoken, err := NewSpokenResponseSink(textSink, synth, player, SpeechOptions{Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if err := spoken.Push(context.Background(), ResponseDelta{UtteranceID: 20, TurnID: 21, Text: "Фраза.\n"}); err != nil {
		t.Fatal(err)
	}
	<-player.started
	type abortOutcome struct {
		result ResponseAbortResult
		err    error
	}
	abortDone := make(chan abortOutcome, 1)
	go func() {
		result, err := spoken.Abort(context.Background(), ResponseAbort{UtteranceID: 20, TurnID: 21})
		abortDone <- abortOutcome{result: result, err: err}
	}()
	<-player.cancelObserved
	select {
	case outcome := <-abortDone:
		t.Fatalf("Abort вернулся до завершения тестового воспроизведения: %v", outcome.err)
	default:
	}
	close(playGate)
	if outcome := <-abortDone; !errors.Is(outcome.err, cleanupErr) || outcome.result.PlayedText != "Фраза.\n" {
		t.Fatalf("Abort() = %+v, %v; ожидались подтверждённая фраза и ошибка очистки консольного вывода", outcome.result, outcome.err)
	}
	if err := spoken.Push(context.Background(), ResponseDelta{UtteranceID: 22, TurnID: 23, Text: "Следующая фраза.\n"}); err != nil {
		t.Fatal(err)
	}
	if err := spoken.Complete(context.Background(), Response{UtteranceID: 22, TurnID: 23, Text: "Следующая фраза.\n"}); err != nil {
		t.Fatalf("new session Complete() error = %v", err)
	}
	if synth.calls.Load() != 2 {
		t.Fatalf("synthesis calls across independent sessions = %d, want 2", synth.calls.Load())
	}
}

func TestSpokenResponseSinkAbortReturnsOnlyFullyPlayedPrefix(t *testing.T) {
	textSink := &recordingResponseSink{}
	synth := &speechTestSynth{texts: make(chan string, 4)}
	player := &interruptSpeechPlayer{started: make(chan int32, 4)}
	spoken, err := NewSpokenResponseSink(textSink, synth, player, SpeechOptions{Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	responseText := "Первое. Второе. Третье."
	if err := spoken.Push(context.Background(), ResponseDelta{UtteranceID: 40, TurnID: 41, Text: responseText}); err != nil {
		t.Fatal(err)
	}
	if call := <-player.started; call != 1 {
		t.Fatalf("номер первого вызова воспроизведения: %d", call)
	}
	if call := <-player.started; call != 2 {
		t.Fatalf("номер второго вызова воспроизведения: %d", call)
	}
	result, err := spoken.Abort(context.Background(), ResponseAbort{UtteranceID: 40, TurnID: 41})
	if err != nil {
		t.Fatal(err)
	}
	if result.PlayedText != "Первое. " {
		t.Fatalf("подтверждённое воспроизведение=%q, ожидалась первая raw-фраза целиком", result.PlayedText)
	}
	if !strings.HasPrefix(responseText, result.PlayedText) || synth.calls.Load() != 2 || player.calls.Load() != 2 {
		t.Fatalf("префикс=%q, вызовов синтеза=%d, вызовов воспроизведения=%d", result.PlayedText, synth.calls.Load(), player.calls.Load())
	}
}

func TestSpokenResponseSinkProgressConcatenatesLimitSplitWithoutAddedSpace(t *testing.T) {
	textSink := &recordingResponseSink{}
	synth := &speechTestSynth{texts: make(chan string, 4)}
	player := &speechTestPlayer{started: make(chan []float32, 4)}
	spoken, err := NewSpokenResponseSink(textSink, synth, player, SpeechOptions{Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Repeat("я", 200) + "я. "
	if err := spoken.Push(context.Background(), ResponseDelta{UtteranceID: 50, TurnID: 51, Text: text}); err != nil {
		t.Fatal(err)
	}
	<-player.started
	<-player.started
	result, err := spoken.Abort(context.Background(), ResponseAbort{UtteranceID: 50, TurnID: 51})
	if err != nil {
		t.Fatal(err)
	}
	if result.PlayedText != text {
		t.Fatalf("подтверждённый raw-префикс=%q, ожидался %q", result.PlayedText, text)
	}
}

func TestSpokenResponseSinkAbortJoinsNativeAndConsoleErrors(t *testing.T) {
	synthErr := errors.New("ошибка нативного синтеза при отмене")
	cleanupErr := errors.New("ошибка очистки консольного вывода")
	textSink := &recordingResponseSink{abortFunc: func(context.Context, ResponseAbort) error { return cleanupErr }}
	synth := &speechTestSynth{
		texts: make(chan string, 1), started: make(chan struct{}), waitForCancel: true, err: synthErr,
	}
	player := &speechTestPlayer{started: make(chan []float32, 1)}
	spoken, err := NewSpokenResponseSink(textSink, synth, player, SpeechOptions{Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if err := spoken.Push(context.Background(), ResponseDelta{UtteranceID: 30, TurnID: 31, Text: "Фраза.\n"}); err != nil {
		t.Fatal(err)
	}
	<-synth.started
	_, err = spoken.Abort(context.Background(), ResponseAbort{UtteranceID: 30, TurnID: 31})
	if !errors.Is(err, synthErr) || !errors.Is(err, cleanupErr) {
		t.Fatalf("Abort() error = %v, want both independent failures", err)
	}
	if player.calls.Load() != 0 {
		t.Fatalf("отменённый PCM передан в Play %d раз", player.calls.Load())
	}
	synth.waitForCancel = false
	synth.err = nil
	synth.started = nil
	if err := spoken.Push(context.Background(), ResponseDelta{UtteranceID: 32, TurnID: 33, Text: "Следующая фраза.\n"}); err != nil {
		t.Fatal(err)
	}
	if err := spoken.Complete(context.Background(), Response{UtteranceID: 32, TurnID: 33, Text: "Следующая фраза.\n"}); err != nil {
		t.Fatalf("следующая сессия после отменённого синтеза: %v", err)
	}
	if player.calls.Load() != 1 {
		t.Fatalf("после безопасного возврата synth Play вызван %d раз", player.calls.Load())
	}
}

func TestSpokenResponseSinkPropagatesTextAndAudioFailures(t *testing.T) {
	consolePushErr := errors.New("ошибка передачи текста в консоль")
	t.Run("console push", func(t *testing.T) {
		textSink := &recordingResponseSink{pushFunc: func(context.Context, ResponseDelta) error { return consolePushErr }}
		synth := &speechTestSynth{texts: make(chan string, 1)}
		player := &speechTestPlayer{started: make(chan []float32, 1)}
		spoken, err := NewSpokenResponseSink(textSink, synth, player, SpeechOptions{Timeout: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		if err := spoken.Push(context.Background(), ResponseDelta{UtteranceID: 1, TurnID: 2, Text: "Фраза.\n"}); !errors.Is(err, consolePushErr) {
			t.Fatalf("Push() error = %v", err)
		}
		if _, err := spoken.Abort(context.Background(), ResponseAbort{UtteranceID: 1, TurnID: 2}); err != nil {
			t.Fatal(err)
		}
		if synth.calls.Load() != 0 {
			t.Fatalf("synth calls after rejected console Push = %d", synth.calls.Load())
		}
	})

	consoleCompleteErr := errors.New("ошибка завершения консольного вывода")
	t.Run("console complete", func(t *testing.T) {
		textSink := &recordingResponseSink{completeFunc: func(context.Context, Response) error { return consoleCompleteErr }}
		synth := &speechTestSynth{texts: make(chan string, 1)}
		player := &speechTestPlayer{started: make(chan []float32, 1)}
		spoken, err := NewSpokenResponseSink(textSink, synth, player, SpeechOptions{Timeout: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		response := Response{UtteranceID: 3, TurnID: 4, Text: "короткий хвост"}
		if err := spoken.Push(context.Background(), ResponseDelta{UtteranceID: 3, TurnID: 4, Text: response.Text}); err != nil {
			t.Fatal(err)
		}
		if err := spoken.Complete(context.Background(), response); !errors.Is(err, consoleCompleteErr) {
			t.Fatalf("Complete() error = %v", err)
		}
		if len(textSink.completes) != 1 || synth.calls.Load() != 1 || player.calls.Load() != 1 {
			t.Fatalf("console completes=%d synth=%d play=%d", len(textSink.completes), synth.calls.Load(), player.calls.Load())
		}
		if _, err := spoken.Abort(context.Background(), ResponseAbort{UtteranceID: 3, TurnID: 4}); err != nil {
			t.Fatal(err)
		}
	})

	for _, test := range []struct {
		name         string
		playerErr    error
		synthesisErr error
	}{
		{name: "синтез", synthesisErr: errors.New("ошибка синтеза")},
		{name: "воспроизведение", playerErr: errors.New("ошибка воспроизведения")},
	} {
		t.Run(test.name, func(t *testing.T) {
			textSink := &recordingResponseSink{}
			synth := &speechTestSynth{texts: make(chan string, 1), started: make(chan struct{}), err: test.synthesisErr}
			player := &speechTestPlayer{started: make(chan []float32, 1), err: test.playerErr}
			spoken, err := NewSpokenResponseSink(textSink, synth, player, SpeechOptions{Timeout: time.Minute})
			if err != nil {
				t.Fatal(err)
			}
			response := Response{UtteranceID: 5, TurnID: 6, Text: "Фраза.\n"}
			pushErr := spoken.Push(context.Background(), ResponseDelta{UtteranceID: 5, TurnID: 6, Text: response.Text})
			if test.synthesisErr != nil {
				<-synth.started
			} else {
				<-player.started
			}
			operationErr := pushErr
			if operationErr == nil {
				operationErr = spoken.Complete(context.Background(), response)
			}
			wantErr := test.synthesisErr
			if wantErr == nil {
				wantErr = test.playerErr
			}
			if !errors.Is(operationErr, wantErr) {
				t.Fatalf("response operation error = %v, want %v", operationErr, wantErr)
			}
			if len(textSink.completes) != 0 {
				t.Fatal("audio failure was accepted as completed response")
			}
			if _, err := spoken.Abort(context.Background(), ResponseAbort{UtteranceID: 5, TurnID: 6}); err != nil {
				t.Fatalf("Abort repeated or lost worker error: %v", err)
			}
		})
	}
}

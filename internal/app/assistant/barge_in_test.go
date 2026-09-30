package assistant

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/service/audio"
	"github.com/Seraf-seraf/voice_assistent/internal/service/control/router"
	"github.com/Seraf-seraf/voice_assistent/internal/service/dialogue"
	"github.com/Seraf-seraf/voice_assistent/internal/service/llm"
	"github.com/Seraf-seraf/voice_assistent/internal/service/stt"
	"github.com/Seraf-seraf/voice_assistent/internal/service/transcript"
	"github.com/Seraf-seraf/voice_assistent/internal/service/tts"
	"github.com/Seraf-seraf/voice_assistent/internal/service/vad"
)

type bargeListener struct {
	events chan vad.Event
	eof    chan struct{}
}

func newBargeListener() *bargeListener {
	return &bargeListener{events: make(chan vad.Event, 8), eof: make(chan struct{})}
}

func (l *bargeListener) Run(ctx context.Context) error {
	select {
	case <-l.eof:
	case <-ctx.Done():
	}
	close(l.events)
	return nil
}

func (l *bargeListener) Events() <-chan vad.Event { return l.events }

type bargeSynth struct {
	texts chan string
}

func (s *bargeSynth) Synthesize(ctx context.Context, text string) (audio.PCM, error) {
	if err := ctx.Err(); err != nil {
		return audio.PCM{}, err
	}
	s.texts <- text
	return audio.PCM{Samples: []float32{1}, SampleRate: 22050}, nil
}

type bargePlayer struct {
	calls       atomic.Int32
	firstPlayed chan struct{}
	secondStart chan struct{}
	secondDone  chan struct{}
	deviceErr   error
}

func (p *bargePlayer) Play(ctx context.Context, _ audio.PCM) error {
	switch p.calls.Add(1) {
	case 1:
		close(p.firstPlayed)
		return nil
	case 2:
		close(p.secondStart)
		<-ctx.Done()
		if p.deviceErr != nil {
			close(p.secondDone)
			return errors.Join(ctx.Err(), p.deviceErr)
		}
		close(p.secondDone)
		return ctx.Err()
	default:
		return nil
	}
}

func TestRuntimeSpeechStartInterruptsSpokenAnswerAndSerializesNextTurn(t *testing.T) {
	listener := newBargeListener()
	manager, responder, spokenText, synth, player, oldStarted, firstCancelled, releaseOld, secondGenerate := newBargeRuntime(t, nil)
	transcriber := newBargeTranscriber(t, responder)
	runtime, err := NewRuntime(listener, transcriber)
	if err != nil {
		t.Fatal(err)
	}
	runDone := make(chan error, 1)
	runCtx, cancelRun := context.WithCancel(context.Background())
	go func() { runDone <- runtime.Run(runCtx) }()
	var releaseOnce, eofOnce sync.Once
	releaseOldFn := func() { releaseOnce.Do(func() { close(releaseOld) }) }
	finishListener := func() { eofOnce.Do(func() { close(listener.eof) }) }
	doneObserved := false
	t.Cleanup(func() {
		cancelRun()
		releaseOldFn()
		finishListener()
		if !doneObserved {
			select {
			case <-runDone:
			case <-time.After(3 * time.Second):
				t.Error("Runtime goroutine did not stop during test cleanup")
			}
		}
	})
	listener.events <- vad.SpeechStarted{}
	listener.events <- vad.SpeechEnded{Utterance: audio.Utterance{ID: 1}}
	waitForSignal(t, oldStarted, "первая генерация не началась")
	select {
	case got := <-synth.texts:
		if got != "Первое." {
			t.Fatalf("синтезировано %q вместо первого предложения", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("синтез первой фразы не начался")
	}
	waitForSignal(t, player.firstPlayed, "первое предложение не проиграно")
	waitForSignal(t, player.secondStart, "второе предложение не началось")
	listener.events <- vad.SpeechStarted{}
	select {
	case <-firstCancelled:
	case <-time.After(2 * time.Second):
		transcriber.mu.Lock()
		active, generation := transcriber.active != nil, transcriber.generation
		transcriber.mu.Unlock()
		select {
		case err := <-runDone:
			doneObserved = true
			t.Fatalf("Runtime завершился до отмены: %v; active=%v generation=%d", err, active, generation)
		default:
			t.Fatalf("отмена не достигла генератора; active=%v generation=%d", active, generation)
		}
	}
	waitForSignal(t, player.secondDone, "проигрыватель не остановился после отмены")
	listener.events <- vad.SpeechEnded{Utterance: audio.Utterance{ID: 2}}
	select {
	case <-secondGenerate:
		t.Fatal("новая генерация началась до возврата старого handler")
	default:
	}
	releaseOldFn()
	waitForSignal(t, secondGenerate, "следующая генерация не началась")
	finishListener()
	select {
	case err := <-runDone:
		doneObserved = true
		if err != nil {
			t.Fatalf("Runtime завершился с ошибкой: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Runtime не завершился после EOF")
	}
	if len(spokenText.completes) != 1 || spokenText.completes[0].Text != "Новый ответ." {
		t.Fatalf("текстовый приёмник завершил ответы: %+v, push=%+v abort=%+v история=%+v", spokenText.completes, spokenText.pushes, spokenText.aborts, manager.Snapshot().Messages)
	}
	wantHistory := []dialogue.Message{
		{Role: dialogue.RoleUser, Content: "первый вопрос"},
		{Role: dialogue.RoleAssistant, Content: "Первое."},
		{Role: dialogue.RoleUser, Content: "второй вопрос"},
		{Role: dialogue.RoleAssistant, Content: "Новый ответ."},
	}
	if got := manager.Snapshot().Messages; !reflect.DeepEqual(got, wantHistory) {
		t.Fatalf("история после прерывания=%+v, ожидалось %+v", got, wantHistory)
	}
	if player.calls.Load() != 3 {
		t.Fatalf("число Play=%d, ожидалось 3 без старой третьей фразы", player.calls.Load())
	}
}

func TestRuntimePreservesPlaybackFailureAlongsideSpeechInterrupt(t *testing.T) {
	listener := newBargeListener()
	deviceErr := errors.New("ошибка аудиоустройства")
	_, responder, _, _, player, oldStarted, firstCancelled, releaseOld, _ := newBargeRuntime(t, deviceErr)
	transcriber := newBargeTranscriber(t, responder)
	runtime, err := NewRuntime(listener, transcriber)
	if err != nil {
		t.Fatal(err)
	}
	runDone := make(chan error, 1)
	runCtx, cancelRun := context.WithCancel(context.Background())
	go func() { runDone <- runtime.Run(runCtx) }()
	var releaseOnce sync.Once
	doneObserved := false
	t.Cleanup(func() {
		cancelRun()
		releaseOnce.Do(func() { close(releaseOld) })
		if !doneObserved {
			select {
			case <-runDone:
			case <-time.After(3 * time.Second):
				t.Error("Runtime goroutine did not stop during test cleanup")
			}
		}
	})
	listener.events <- vad.SpeechStarted{}
	listener.events <- vad.SpeechEnded{Utterance: audio.Utterance{ID: 1}}
	waitForSignal(t, oldStarted, "первая генерация не началась")
	waitForSignal(t, player.secondStart, "второе предложение не началось")
	listener.events <- vad.SpeechStarted{}
	select {
	case <-firstCancelled:
	case <-time.After(2 * time.Second):
		transcriber.mu.Lock()
		active, generation := transcriber.active != nil, transcriber.generation
		transcriber.mu.Unlock()
		select {
		case err := <-runDone:
			doneObserved = true
			t.Fatalf("Runtime завершился до отмены: %v; active=%v generation=%d", err, active, generation)
		default:
			t.Fatalf("отмена не достигла генератора; active=%v generation=%d", active, generation)
		}
	}
	waitForSignal(t, player.secondDone, "проигрыватель не вернулся после отмены")
	releaseOnce.Do(func() { close(releaseOld) })
	select {
	case err := <-runDone:
		doneObserved = true
		if !errors.Is(err, deviceErr) {
			t.Fatalf("Runtime потерял ошибку устройства: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Runtime не вернул независимую ошибку воспроизведения")
	}
}

func TestRuntimeTextOnlyInterruptStopAndNextQuery(t *testing.T) {
	listener := newBargeListener()
	manager, err := dialogue.New(dialogue.Options{SystemPrompt: "system", ResponsePolicy: "policy", MaxHistoryMessages: 12})
	if err != nil {
		t.Fatal(err)
	}
	textSink := &recordingResponseSink{}
	firstStarted := make(chan struct{})
	firstCancelled := make(chan struct{})
	releaseFirst := make(chan struct{})
	stopRecognized := make(chan struct{})
	queryStarted := make(chan struct{})
	var generatorCalls atomic.Int32
	generator := fakeGenerator{generate: func(ctx context.Context, _ llm.Request, emit llm.Emit) error {
		switch generatorCalls.Add(1) {
		case 1:
			if err := emit(llm.TextDelta{Text: "Частичный ответ."}); err != nil {
				return err
			}
			close(firstStarted)
			<-ctx.Done()
			close(firstCancelled)
			<-releaseFirst
			return ctx.Err()
		case 2:
			close(queryStarted)
			return emit(llm.TextDelta{Text: "Новый ответ."})
		default:
			return errors.New("неожиданный повтор генерации")
		}
	}}
	responder, err := NewResponder(manager, generator, llm.Options{Temperature: 0.4, MaxTokens: 30}, textSink)
	if err != nil {
		t.Fatal(err)
	}
	normalizer, err := transcript.NewNormalizer(transcript.Options{MinSignificantRunes: 2})
	if err != nil {
		t.Fatal(err)
	}
	controlRouter, err := router.New(router.Options{Mode: router.ModeAlways})
	if err != nil {
		t.Fatal(err)
	}
	processor, err := NewInputProcessor(normalizer, controlRouter, manager, responder.Handle, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	client := fakeSTTClient{transcribe: func(_ context.Context, utterance audio.Utterance) (stt.Transcript, error) {
		switch utterance.ID {
		case 1:
			return stt.Transcript{Text: "первый вопрос"}, nil
		case 2:
			return stt.Transcript{Text: "стоп"}, nil
		default:
			return stt.Transcript{Text: "следующий вопрос"}, nil
		}
	}}
	transcriber, err := NewTranscriber(client, func(ctx context.Context, result Transcription) error {
		if result.UtteranceID == 2 {
			close(stopRecognized)
		}
		return processor.Handle(ctx, result)
	}, 2)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(listener, transcriber)
	if err != nil {
		t.Fatal(err)
	}
	runDone := make(chan error, 1)
	runCtx, cancelRun := context.WithCancel(context.Background())
	go func() { runDone <- runtime.Run(runCtx) }()
	var releaseOnce, eofOnce sync.Once
	finishListener := func() { eofOnce.Do(func() { close(listener.eof) }) }
	releaseFirstFn := func() { releaseOnce.Do(func() { close(releaseFirst) }) }
	doneObserved := false
	t.Cleanup(func() {
		cancelRun()
		releaseFirstFn()
		finishListener()
		if !doneObserved {
			select {
			case <-runDone:
			case <-time.After(3 * time.Second):
				t.Error("Runtime goroutine did not stop during test cleanup")
			}
		}
	})
	listener.events <- vad.SpeechStarted{}
	listener.events <- vad.SpeechEnded{Utterance: audio.Utterance{ID: 1}}
	waitForSignal(t, firstStarted, "первая генерация не началась")
	listener.events <- vad.SpeechStarted{}
	waitForSignal(t, firstCancelled, "текстовый ответ не отменён")
	releaseFirstFn()
	listener.events <- vad.SpeechEnded{Utterance: audio.Utterance{ID: 2}}
	waitForSignal(t, stopRecognized, "команда остановки не распознана")
	if got := generatorCalls.Load(); got != 1 {
		t.Fatalf("команда остановки запустила генераций: %d", got)
	}
	listener.events <- vad.SpeechEnded{Utterance: audio.Utterance{ID: 3}}
	waitForSignal(t, queryStarted, "следующий обычный запрос не запустил генерацию")
	finishListener()
	select {
	case err := <-runDone:
		doneObserved = true
		if err != nil {
			t.Fatalf("Runtime завершился с ошибкой: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Runtime не завершился после EOF")
	}
	want := []dialogue.Message{
		{Role: dialogue.RoleUser, Content: "первый вопрос"},
		{Role: dialogue.RoleUser, Content: "следующий вопрос"},
		{Role: dialogue.RoleAssistant, Content: "Новый ответ."},
	}
	if got := manager.Snapshot().Messages; !reflect.DeepEqual(got, want) {
		t.Fatalf("история text-only пути=%+v, ожидалось %+v", got, want)
	}
	if generatorCalls.Load() != 2 || len(textSink.completes) != 1 {
		t.Fatalf("генераций=%d, завершений вывода=%d", generatorCalls.Load(), len(textSink.completes))
	}
}

func newBargeRuntime(t *testing.T, deviceErr error) (*dialogue.Manager, *Responder, *recordingResponseSink, *bargeSynth, *bargePlayer, <-chan struct{}, <-chan struct{}, chan struct{}, <-chan struct{}) {
	t.Helper()
	manager, err := dialogue.New(dialogue.Options{SystemPrompt: "system", ResponsePolicy: "policy", MaxHistoryMessages: 12})
	if err != nil {
		t.Fatal(err)
	}
	textSink := &recordingResponseSink{}
	synth := &bargeSynth{texts: make(chan string, 8)}
	player := &bargePlayer{
		firstPlayed: make(chan struct{}), secondStart: make(chan struct{}), secondDone: make(chan struct{}), deviceErr: deviceErr,
	}
	spoken, err := NewSpokenResponseSink(textSink, synth, player, SpeechOptions{Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	oldStarted := make(chan struct{})
	oldCancelled := make(chan struct{})
	releaseOld := make(chan struct{})
	secondGenerate := make(chan struct{})
	var generateCalls atomic.Int32
	generator := fakeGenerator{generate: func(ctx context.Context, request llm.Request, emit llm.Emit) error {
		switch generateCalls.Add(1) {
		case 1:
			if err := emit(llm.TextDelta{Text: "Первое. Второе. Третье. Х"}); err != nil {
				return err
			}
			close(oldStarted)
			select {
			case <-player.firstPlayed:
			case <-ctx.Done():
				close(oldCancelled)
				return ctx.Err()
			}
			select {
			case <-player.secondStart:
			case <-ctx.Done():
				close(oldCancelled)
				return ctx.Err()
			}
			<-ctx.Done()
			close(oldCancelled)
			<-releaseOld
			return ctx.Err()
		case 2:
			err := emit(llm.TextDelta{Text: "Новый ответ."})
			close(secondGenerate)
			return err
		default:
			return errors.New("запущена лишняя генерация")
		}
	}}
	responder, err := NewResponder(manager, generator, llm.Options{Temperature: 0.4, MaxTokens: 100}, spoken)
	if err != nil {
		t.Fatal(err)
	}
	return manager, responder, textSink, synth, player, oldStarted, oldCancelled, releaseOld, secondGenerate
}

func newBargeTranscriber(t *testing.T, responder *Responder) *Transcriber {
	t.Helper()
	normalizer, err := transcript.NewNormalizer(transcript.Options{MinSignificantRunes: 2})
	if err != nil {
		t.Fatal(err)
	}
	controlRouter, err := router.New(router.Options{Mode: router.ModeAlways})
	if err != nil {
		t.Fatal(err)
	}
	processor, err := NewInputProcessor(normalizer, controlRouter, responder.dialogue, responder.Handle, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	client := fakeSTTClient{transcribe: func(_ context.Context, utterance audio.Utterance) (stt.Transcript, error) {
		if utterance.ID == 1 {
			return stt.Transcript{Text: "первый вопрос"}, nil
		}
		return stt.Transcript{Text: "второй вопрос"}, nil
	}}
	transcriber, err := NewTranscriber(client, processor.Handle, 2)
	if err != nil {
		t.Fatal(err)
	}
	return transcriber
}

func waitForSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatal(message)
	}
}

var _ tts.Synthesizer = (*bargeSynth)(nil)

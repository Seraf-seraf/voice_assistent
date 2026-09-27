package assistant

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/audio"
	"github.com/Seraf-seraf/voice_assistent/internal/stt"
	"github.com/Seraf-seraf/voice_assistent/internal/vad"
)

type fakeSTTClient struct {
	transcribe func(context.Context, audio.Utterance) (stt.Transcript, error)
}

func (c fakeSTTClient) Transcribe(ctx context.Context, utterance audio.Utterance) (stt.Transcript, error) {
	return c.transcribe(ctx, utterance)
}

func TestTranscriberQueuesEndedEventsAndEmitsRawResultsInOrder(t *testing.T) {
	first, second := audio.Utterance{ID: 1, Samples: []int16{5, 9}}, audio.Utterance{ID: 2, Samples: []int16{7}}
	var mu sync.Mutex
	var seen []audio.Utterance
	active, maxActive := 0, 0
	client := fakeSTTClient{transcribe: func(_ context.Context, utterance audio.Utterance) (stt.Transcript, error) {
		mu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		seen = append(seen, utterance)
		mu.Unlock()
		defer func() { mu.Lock(); active--; mu.Unlock() }()
		return stt.Transcript{Text: " raw ", Duration: 17 * time.Millisecond}, nil
	}}
	var results []Transcription
	transcriber, err := NewTranscriber(client, func(_ context.Context, result Transcription) error {
		results = append(results, result)
		return nil
	}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := transcriber.Handle(context.Background(), vad.SpeechStarted{}); err != nil {
		t.Fatal(err)
	}
	for _, utterance := range []audio.Utterance{first, second} {
		if err := transcriber.Handle(context.Background(), vad.SpeechEnded{Utterance: utterance}); err != nil {
			t.Fatal(err)
		}
	}
	transcriber.CloseInput()
	if err := transcriber.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(seen, []audio.Utterance{first, second}) || maxActive != 1 {
		t.Fatalf("seen=%v max concurrency=%d", seen, maxActive)
	}
	want := []Transcription{{UtteranceID: 1, Text: " raw ", Duration: 17 * time.Millisecond}, {UtteranceID: 2, Text: " raw ", Duration: 17 * time.Millisecond}}
	if !reflect.DeepEqual(results, want) {
		t.Fatalf("results=%+v want %+v", results, want)
	}
}

func TestTranscriberQueueBackpressureRespectsContext(t *testing.T) {
	transcriber, err := NewTranscriber(fakeSTTClient{transcribe: func(context.Context, audio.Utterance) (stt.Transcript, error) {
		return stt.Transcript{}, nil
	}}, func(context.Context, Transcription) error { return nil }, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := transcriber.Handle(context.Background(), vad.SpeechEnded{Utterance: audio.Utterance{ID: 1}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := transcriber.Handle(ctx, vad.SpeechEnded{Utterance: audio.Utterance{ID: 2}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Handle() error = %v, want context.Canceled", err)
	}
	transcriber.CloseInput()
	if err := transcriber.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestTranscriberErrorsAndCancellation(t *testing.T) {
	wantErr := errors.New("ошибка STT")
	transcriber, _ := NewTranscriber(fakeSTTClient{transcribe: func(context.Context, audio.Utterance) (stt.Transcript, error) { return stt.Transcript{}, wantErr }}, func(context.Context, Transcription) error { return nil }, 1)
	_ = transcriber.Handle(context.Background(), vad.SpeechEnded{Utterance: audio.Utterance{ID: 1}})
	transcriber.CloseInput()
	if err := transcriber.Run(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("Run error=%v", err)
	}

	handlerErr := errors.New("ошибка обработчика")
	transcriber, _ = NewTranscriber(fakeSTTClient{transcribe: func(context.Context, audio.Utterance) (stt.Transcript, error) { return stt.Transcript{}, nil }}, func(context.Context, Transcription) error { return handlerErr }, 1)
	_ = transcriber.Handle(context.Background(), vad.SpeechEnded{Utterance: audio.Utterance{ID: 2}})
	transcriber.CloseInput()
	if err := transcriber.Run(context.Background()); !errors.Is(err, handlerErr) {
		t.Fatalf("Run error=%v", err)
	}

	started := make(chan struct{})
	transcriber, _ = NewTranscriber(fakeSTTClient{transcribe: func(ctx context.Context, _ audio.Utterance) (stt.Transcript, error) {
		close(started)
		<-ctx.Done()
		return stt.Transcript{}, ctx.Err()
	}}, func(context.Context, Transcription) error { return nil }, 1)
	_ = transcriber.Handle(context.Background(), vad.SpeechEnded{Utterance: audio.Utterance{ID: 3}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- transcriber.Run(ctx) }()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error=%v", err)
	}
}

func TestNewTranscriberRejectsInvalidDependenciesAndCloseInputIsIdempotent(t *testing.T) {
	client := fakeSTTClient{transcribe: func(context.Context, audio.Utterance) (stt.Transcript, error) { return stt.Transcript{}, nil }}
	handler := TranscriptionHandler(func(context.Context, Transcription) error { return nil })
	var nilClient *fakeSTTClient
	for _, args := range []struct {
		client  stt.Client
		handler TranscriptionHandler
		size    int
	}{{nil, handler, 1}, {nilClient, handler, 1}, {client, nil, 1}, {client, handler, 0}, {client, handler, -1}} {
		if _, err := NewTranscriber(args.client, args.handler, args.size); err == nil {
			t.Fatal("NewTranscriber() accepted invalid input")
		}
	}
	transcriber, err := NewTranscriber(client, handler, 1)
	if err != nil {
		t.Fatal(err)
	}
	transcriber.CloseInput()
	transcriber.CloseInput()
	if err := transcriber.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestTranscriberSpeechStartCancelsActiveHandlerWithoutStartingAnother(t *testing.T) {
	entered := make(chan Transcription, 2)
	ctxCancelled := make(chan struct{})
	releaseFirst := make(chan struct{})
	var releaseFirstOnce sync.Once
	transcriber, err := NewTranscriber(fakeSTTClient{transcribe: func(context.Context, audio.Utterance) (stt.Transcript, error) {
		return stt.Transcript{Text: "текст"}, nil
	}}, func(ctx context.Context, result Transcription) error {
		entered <- result
		if result.UtteranceID == 1 {
			<-ctx.Done()
			close(ctxCancelled)
			<-releaseFirst
			return ctx.Err()
		}
		return nil
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	runDone := make(chan error, 1)
	go func() { runDone <- transcriber.Run(context.Background()) }()
	runDoneObserved := false
	t.Cleanup(func() {
		releaseFirstOnce.Do(func() { close(releaseFirst) })
		transcriber.CloseInput()
		if !runDoneObserved {
			<-runDone
		}
	})
	if err := transcriber.Handle(context.Background(), vad.SpeechEnded{Utterance: audio.Utterance{ID: 1}}); err != nil {
		t.Fatal(err)
	}
	if got := <-entered; got.UtteranceID != 1 {
		t.Fatalf("первый обработчик получил %+v", got)
	}
	if err := transcriber.Handle(context.Background(), vad.SpeechStarted{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctxCancelled:
	case <-time.After(time.Second):
		t.Fatal("контекст активного обработчика не отменён")
	}
	if err := transcriber.Handle(context.Background(), vad.SpeechEnded{Utterance: audio.Utterance{ID: 2}}); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-entered:
		t.Fatalf("второй обработчик запущен до возврата первого: %+v", got)
	default:
	}
	releaseFirstOnce.Do(func() { close(releaseFirst) })
	if got := <-entered; got.UtteranceID != 2 {
		t.Fatalf("второй обработчик получил %+v", got)
	}
	transcriber.CloseInput()
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	runDoneObserved = true
}

func TestTranscriberSpeechStartReplacesQueuedGenerationAndQueueFullIsImmediate(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	var mu sync.Mutex
	var seen []uint64
	transcriber, err := NewTranscriber(fakeSTTClient{transcribe: func(_ context.Context, utterance audio.Utterance) (stt.Transcript, error) {
		mu.Lock()
		seen = append(seen, utterance.ID)
		first := len(seen) == 1
		mu.Unlock()
		if first {
			close(started)
			<-release
		}
		return stt.Transcript{}, nil
	}}, func(context.Context, Transcription) error { return nil }, 1)
	if err != nil {
		t.Fatal(err)
	}
	runDone := make(chan error, 1)
	go func() { runDone <- transcriber.Run(context.Background()) }()
	runDoneObserved := false
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		transcriber.CloseInput()
		if !runDoneObserved {
			<-runDone
		}
	})
	if err := transcriber.Handle(context.Background(), vad.SpeechEnded{Utterance: audio.Utterance{ID: 1}}); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := transcriber.Handle(context.Background(), vad.SpeechEnded{Utterance: audio.Utterance{ID: 2}}); err != nil {
		t.Fatal(err)
	}
	if err := transcriber.Handle(context.Background(), vad.SpeechEnded{Utterance: audio.Utterance{ID: 3}}); !errors.Is(err, ErrTranscriptionQueueFull) {
		t.Fatalf("ошибка полной очереди=%v", err)
	}
	if err := transcriber.Handle(context.Background(), vad.SpeechStarted{}); err != nil {
		t.Fatal(err)
	}
	if err := transcriber.Handle(context.Background(), vad.SpeechEnded{Utterance: audio.Utterance{ID: 4}}); err != nil {
		t.Fatalf("новое поколение не принято: %v", err)
	}
	releaseOnce.Do(func() { close(release) })
	transcriber.CloseInput()
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	runDoneObserved = true
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(seen, []uint64{1, 4}) {
		t.Fatalf("обработанные реплики=%v, ожидалось [1 4]", seen)
	}
}

func TestTranscriberInputClosedAndGenerationOverflow(t *testing.T) {
	transcriber, err := NewTranscriber(fakeSTTClient{transcribe: func(context.Context, audio.Utterance) (stt.Transcript, error) {
		return stt.Transcript{}, nil
	}}, func(context.Context, Transcription) error { return nil }, 1)
	if err != nil {
		t.Fatal(err)
	}
	transcriber.generation = maxInputGeneration - 1
	if err := transcriber.Handle(context.Background(), vad.SpeechStarted{}); err != nil {
		t.Fatal(err)
	}
	if err := transcriber.Handle(context.Background(), vad.SpeechStarted{}); !errors.Is(err, ErrTranscriptionGeneration) {
		t.Fatalf("ошибка переполнения поколения=%v", err)
	}
	transcriber.CloseInput()
	transcriber.CloseInput()
	if err := transcriber.Handle(context.Background(), vad.SpeechEnded{}); !errors.Is(err, ErrTranscriptionInputClosed) {
		t.Fatalf("ошибка закрытого входа=%v", err)
	}
	if err := transcriber.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestTranscriberLateSuccessfulSTTIsNotHandledAfterInterrupt(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	handled := make(chan struct{}, 1)
	transcriber, err := NewTranscriber(fakeSTTClient{transcribe: func(ctx context.Context, _ audio.Utterance) (stt.Transcript, error) {
		close(started)
		<-ctx.Done()
		<-release
		return stt.Transcript{Text: "поздний результат"}, nil
	}}, func(context.Context, Transcription) error { handled <- struct{}{}; return nil }, 1)
	if err != nil {
		t.Fatal(err)
	}
	runDone := make(chan error, 1)
	go func() { runDone <- transcriber.Run(context.Background()) }()
	runDoneObserved := false
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		transcriber.CloseInput()
		if !runDoneObserved {
			<-runDone
		}
	})
	if err := transcriber.Handle(context.Background(), vad.SpeechEnded{Utterance: audio.Utterance{ID: 1}}); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := transcriber.Handle(context.Background(), vad.SpeechStarted{}); err != nil {
		t.Fatal(err)
	}
	releaseOnce.Do(func() { close(release) })
	transcriber.CloseInput()
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	runDoneObserved = true
	select {
	case <-handled:
		t.Fatal("поздний результат STT передан обработчику")
	default:
	}
}

func TestTranscriberReturnsIndependentHandlerFailureDuringInterrupt(t *testing.T) {
	sentinel := errors.New("ошибка обработчика")
	entered := make(chan struct{})
	transcriber, err := NewTranscriber(fakeSTTClient{transcribe: func(context.Context, audio.Utterance) (stt.Transcript, error) {
		return stt.Transcript{}, nil
	}}, func(ctx context.Context, _ Transcription) error {
		close(entered)
		<-ctx.Done()
		return errors.Join(ctx.Err(), sentinel)
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := transcriber.Handle(context.Background(), vad.SpeechEnded{Utterance: audio.Utterance{ID: 1}}); err != nil {
		t.Fatal(err)
	}
	runDone := make(chan error, 1)
	go func() { runDone <- transcriber.Run(context.Background()) }()
	<-entered
	if err := transcriber.Handle(context.Background(), vad.SpeechStarted{}); err != nil {
		t.Fatal(err)
	}
	if err := <-runDone; !errors.Is(err, sentinel) {
		t.Fatalf("Run() потерял независимую ошибку handler: %v", err)
	}
	transcriber.CloseInput()
}

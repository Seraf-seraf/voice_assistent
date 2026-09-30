package assistant

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/service/audio"
	"github.com/Seraf-seraf/voice_assistent/internal/service/diagnostics"
	"github.com/Seraf-seraf/voice_assistent/internal/service/stt"
	"github.com/Seraf-seraf/voice_assistent/internal/service/vad"
)

const maxInputGeneration uint64 = ^uint64(0)

var (
	ErrTranscriptionInputClosed = errors.New("вход транскрипции закрыт")
	ErrTranscriptionQueueFull   = errors.New("очередь транскрипции заполнена")
	ErrTranscriptionGeneration  = errors.New("исчерпан счётчик поколений речи")
)

type Transcription struct {
	UtteranceID uint64
	Text        string
	Duration    time.Duration
	EndedAt     time.Time
}

type TranscriptionHandler func(context.Context, Transcription) error

type transcriptionJob struct {
	generation uint64
	utterance  audio.Utterance
}

type activeTranscription struct {
	generation uint64
	cancel     context.CancelCauseFunc
}

type Transcriber struct {
	client  stt.Client
	handler TranscriptionHandler
	jobs    chan transcriptionJob

	mu          sync.Mutex
	generation  uint64
	inputClosed bool
	active      *activeTranscription
}

func NewTranscriber(client stt.Client, handler TranscriptionHandler, queueSize int) (*Transcriber, error) {
	if client == nil || isNilDependency(client) {
		return nil, errors.New("клиент STT обязателен")
	}
	if handler == nil {
		return nil, errors.New("обработчик распознанного текста обязателен")
	}
	if queueSize <= 0 {
		return nil, errors.New("размер очереди транскрипции должен быть положительным")
	}
	return &Transcriber{client: client, handler: handler, jobs: make(chan transcriptionJob, queueSize)}, nil
}

func (t *Transcriber) Handle(ctx context.Context, event vad.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch speechEvent := event.(type) {
	case vad.SpeechStarted:
		t.mu.Lock()
		if t.inputClosed {
			t.mu.Unlock()
			return ErrTranscriptionInputClosed
		}
		if t.generation == maxInputGeneration {
			t.mu.Unlock()
			return ErrTranscriptionGeneration
		}
		t.generation++
		for {
			select {
			case <-t.jobs:
			default:
				goto queueDrained
			}
		}
	queueDrained:
		var cancel context.CancelCauseFunc
		if t.active != nil {
			cancel = t.active.cancel
		}
		t.mu.Unlock()
		if cancel != nil {
			cancel(ErrSpeechInterrupted)
		}
		return nil
	case vad.SpeechEnded:
		t.mu.Lock()
		defer t.mu.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		if t.inputClosed {
			return ErrTranscriptionInputClosed
		}
		select {
		case t.jobs <- transcriptionJob{generation: t.generation, utterance: speechEvent.Utterance}:
			return nil
		default:
			return ErrTranscriptionQueueFull
		}
	default:
		return nil
	}
}

func (t *Transcriber) CloseInput() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.inputClosed {
		t.inputClosed = true
		close(t.jobs)
	}
}

func (t *Transcriber) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case job, open := <-t.jobs:
			if !open {
				if err := ctx.Err(); err != nil {
					return err
				}
				return nil
			}
			active, jobCtx, ok := t.startJob(ctx, job)
			if !ok {
				continue
			}
			outcome := t.runJob(jobCtx, job)
			cause := context.Cause(jobCtx)
			t.finishJob(active)
			active.cancel(nil)

			if parentErr := ctx.Err(); parentErr != nil {
				if outcome == nil || cancellationOnly(outcome, context.Canceled, ErrSpeechInterrupted) {
					return parentErr
				}
				return errors.Join(parentErr, outcome)
			}
			if cause == ErrSpeechInterrupted && cancellationOnly(outcome, context.Canceled, ErrSpeechInterrupted) {
				continue
			}
			if outcome != nil {
				return outcome
			}
		}
	}
}

func (t *Transcriber) startJob(runCtx context.Context, job transcriptionJob) (*activeTranscription, context.Context, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := runCtx.Err(); err != nil || job.generation != t.generation {
		return nil, nil, false
	}
	jobCtx, cancel := context.WithCancelCause(runCtx)
	active := &activeTranscription{generation: job.generation, cancel: cancel}
	t.active = active
	return active, jobCtx, true
}

func (t *Transcriber) runJob(jobCtx context.Context, job transcriptionJob) error {
	if err := jobCtx.Err(); err != nil {
		return err
	}
	requestCtx := diagnostics.WithIDs(jobCtx, job.utterance.ID, 0)
	if !job.utterance.EndedAt.IsZero() {
		requestCtx = diagnostics.WithSpeechEnd(requestCtx, job.utterance.EndedAt)
	}
	started := time.Now()
	diagnostics.Emit(requestCtx, diagnostics.Event{Phase: "stt_started"})
	result, err := t.client.Transcribe(requestCtx, job.utterance)
	if err != nil {
		if !cancellationOnly(err, context.Canceled, ErrSpeechInterrupted) {
			diagnostics.Emit(requestCtx, diagnostics.Event{Phase: "stt_failed", Duration: time.Since(started)})
		}
		return fmt.Errorf("распознать реплику %d: %w", job.utterance.ID, err)
	}
	if err := jobCtx.Err(); err != nil {
		return err
	}
	diagnostics.Emit(requestCtx, diagnostics.Event{Phase: "stt_completed", Duration: time.Since(started)})
	if !job.utterance.EndedAt.IsZero() {
		diagnostics.Emit(requestCtx, diagnostics.Event{Phase: "speech_end_to_stt", Duration: time.Since(job.utterance.EndedAt)})
	}
	transcription := Transcription{UtteranceID: job.utterance.ID, Text: result.Text, Duration: result.Duration, EndedAt: job.utterance.EndedAt}
	if err := t.handler(requestCtx, transcription); err != nil {
		return fmt.Errorf("обработать распознанный текст %d: %w", job.utterance.ID, err)
	}
	return nil
}

func (t *Transcriber) finishJob(active *activeTranscription) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.active == active {
		t.active = nil
	}
}

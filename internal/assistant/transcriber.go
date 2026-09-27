package assistant

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/audio"
	"github.com/Seraf-seraf/voice_assistent/internal/stt"
	"github.com/Seraf-seraf/voice_assistent/internal/vad"
)

type Transcription struct {
	UtteranceID uint64
	Text        string
	Duration    time.Duration
}

type TranscriptionHandler func(context.Context, Transcription) error

type Transcriber struct {
	client  stt.Client
	handler TranscriptionHandler
	jobs    chan audio.Utterance
	close   sync.Once
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
	return &Transcriber{client: client, handler: handler, jobs: make(chan audio.Utterance, queueSize)}, nil
}

func (t *Transcriber) Handle(ctx context.Context, event vad.Event) error {
	speechEnded, ok := event.(vad.SpeechEnded)
	if !ok {
		return nil
	}
	select {
	case t.jobs <- speechEnded.Utterance:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *Transcriber) CloseInput() { t.close.Do(func() { close(t.jobs) }) }

func (t *Transcriber) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case utterance, open := <-t.jobs:
			if !open {
				return nil
			}
			result, err := t.client.Transcribe(ctx, utterance)
			if err != nil {
				return fmt.Errorf("распознать реплику %d: %w", utterance.ID, err)
			}
			transcription := Transcription{UtteranceID: utterance.ID, Text: result.Text, Duration: result.Duration}
			if err := t.handler(ctx, transcription); err != nil {
				return fmt.Errorf("обработать распознанный текст %d: %w", utterance.ID, err)
			}
		}
	}
}

package listener

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/Seraf-seraf/voice_assistent/internal/audio"
	"github.com/Seraf-seraf/voice_assistent/internal/audio/input"
	"github.com/Seraf-seraf/voice_assistent/internal/vad"
)

var (
	ErrRunning = errors.New("слушатель уже запущен")
	ErrClosed  = errors.New("слушатель уже закрыт")
)

const (
	listenerCreated uint32 = iota
	listenerRunning
	listenerClosed
)

type Listener struct {
	source    input.Source
	detector  vad.Detector
	segmenter *vad.Segmenter
	events    chan vad.Event
	state     atomic.Uint32
}

func New(source input.Source, detector vad.Detector, segmenter *vad.Segmenter, eventQueueSize int) (*Listener, error) {
	if source == nil {
		return nil, errors.New("источник аудио обязателен")
	}
	if detector == nil {
		return nil, errors.New("детектор VAD обязателен")
	}
	if segmenter == nil {
		return nil, errors.New("сегментатор VAD обязателен")
	}
	if eventQueueSize <= 0 {
		return nil, errors.New("размер очереди событий должен быть положительным")
	}
	return &Listener{
		source: source, detector: detector, segmenter: segmenter,
		events: make(chan vad.Event, eventQueueSize),
	}, nil
}

func (l *Listener) Events() <-chan vad.Event {
	return l.events
}

func (l *Listener) Run(ctx context.Context) (resultErr error) {
	if !l.state.CompareAndSwap(listenerCreated, listenerRunning) {
		if l.state.Load() == listenerRunning {
			return ErrRunning
		}
		return ErrClosed
	}
	defer func() {
		l.state.Store(listenerClosed)
		close(l.events)
	}()
	defer func() {
		if err := l.detector.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("закрыть детектор VAD: %w", err))
		}
	}()
	defer func() {
		if err := l.source.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("закрыть источник аудио: %w", err))
		}
	}()

	sourceErrors := make(chan error, 1)
	go func() {
		sourceErrors <- l.source.Run(ctx)
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-sourceErrors:
			if err != nil {
				return fmt.Errorf("получить аудиокадры: %w", err)
			}
			return nil
		case frame, open := <-l.source.Frames():
			if !open {
				err := <-sourceErrors
				if err != nil {
					return fmt.Errorf("получить аудиокадры: %w", err)
				}
				return nil
			}
			if err := l.processFrame(ctx, frame); err != nil {
				return err
			}
		}
	}
}

func (l *Listener) processFrame(ctx context.Context, frame audio.Frame) error {
	activity, err := l.detector.Classify(frame)
	if err != nil {
		return fmt.Errorf("классифицировать аудиокадр: %w", err)
	}
	events, err := l.segmenter.Process(frame, activity)
	if err != nil {
		return fmt.Errorf("разделить аудиокадр на сегменты: %w", err)
	}
	for _, event := range events {
		select {
		case l.events <- event:
		case <-ctx.Done():
			return nil
		}
	}
	return nil
}

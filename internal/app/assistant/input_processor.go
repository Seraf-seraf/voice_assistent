package assistant

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/service/control/router"
	"github.com/Seraf-seraf/voice_assistent/internal/service/diagnostics"
	"github.com/Seraf-seraf/voice_assistent/internal/service/dialogue"
	"github.com/Seraf-seraf/voice_assistent/internal/service/transcript"
)

type Query struct {
	UtteranceID uint64
	Text        string
	Canonical   string
	EndedAt     time.Time
}

type QueryHandler func(context.Context, Query) error

type Clock func() time.Time

type InputProcessor struct {
	normalizer   *transcript.Normalizer
	router       *router.Router
	dialogue     *dialogue.Manager
	queryHandler QueryHandler
	now          Clock
}

func NewInputProcessor(
	normalizer *transcript.Normalizer,
	controlRouter *router.Router,
	manager *dialogue.Manager,
	queryHandler QueryHandler,
	now Clock,
) (*InputProcessor, error) {
	if normalizer == nil {
		return nil, errors.New("нормализатор транскрипта обязателен")
	}
	if controlRouter == nil {
		return nil, errors.New("маршрутизатор команд обязателен")
	}
	if manager == nil {
		return nil, errors.New("менеджер диалога обязателен")
	}
	if queryHandler == nil {
		return nil, errors.New("обработчик запросов обязателен")
	}
	if now == nil {
		return nil, errors.New("источник времени обязателен")
	}
	return &InputProcessor{
		normalizer: normalizer, router: controlRouter, dialogue: manager,
		queryHandler: queryHandler, now: now,
	}, nil
}

func (p *InputProcessor) Handle(ctx context.Context, transcription Transcription) error {
	normalized, err := p.normalizer.Normalize(transcription.Text)
	if err != nil {
		if errors.Is(err, transcript.ErrEmpty) || errors.Is(err, transcript.ErrTooShort) || errors.Is(err, transcript.ErrFiltered) {
			return nil
		}
		return fmt.Errorf("нормализовать распознанный текст %d: %w", transcription.UtteranceID, err)
	}
	decision := p.router.Route(normalized, p.now())
	switch decision.Kind {
	case router.DecisionIgnored, router.DecisionAwaitingWake:
		return nil
	case router.DecisionQuery:
		queryCtx := diagnostics.WithIDs(ctx, transcription.UtteranceID, 0)
		if !transcription.EndedAt.IsZero() {
			queryCtx = diagnostics.WithSpeechEnd(queryCtx, transcription.EndedAt)
		}
		diagnostics.Emit(queryCtx, diagnostics.Event{Phase: "query_started"})
		if !transcription.EndedAt.IsZero() {
			diagnostics.Emit(queryCtx, diagnostics.Event{Phase: "speech_end_to_query", Duration: time.Since(transcription.EndedAt)})
		}
		if err := p.queryHandler(queryCtx, Query{
			UtteranceID: transcription.UtteranceID,
			Text:        decision.Text,
			Canonical:   decision.Canonical,
			EndedAt:     transcription.EndedAt,
		}); err != nil {
			return fmt.Errorf("обработать запрос для реплики %d: %w", transcription.UtteranceID, err)
		}
		return nil
	case router.DecisionCommand:
		switch decision.Command {
		case router.CommandStop, router.CommandPause, router.CommandResume:
			return nil
		case router.CommandResetHistory:
			p.dialogue.Reset()
			return nil
		default:
			return fmt.Errorf("неизвестная команда маршрутизатора %d", decision.Command)
		}
	default:
		return fmt.Errorf("неизвестный результат маршрутизации %d", decision.Kind)
	}
}

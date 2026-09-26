package assistant

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/control/router"
	"github.com/Seraf-seraf/voice_assistent/internal/dialogue"
	"github.com/Seraf-seraf/voice_assistent/internal/transcript"
)

type Query struct {
	UtteranceID uint64
	Text        string
	Canonical   string
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
		return nil, errors.New("transcript normalizer обязателен")
	}
	if controlRouter == nil {
		return nil, errors.New("control router обязателен")
	}
	if manager == nil {
		return nil, errors.New("dialogue manager обязателен")
	}
	if queryHandler == nil {
		return nil, errors.New("query handler обязателен")
	}
	if now == nil {
		return nil, errors.New("clock обязателен")
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
		return fmt.Errorf("normalize transcription %d: %w", transcription.UtteranceID, err)
	}
	decision := p.router.Route(normalized, p.now())
	switch decision.Kind {
	case router.DecisionIgnored, router.DecisionAwaitingWake:
		return nil
	case router.DecisionQuery:
		if err := p.queryHandler(ctx, Query{
			UtteranceID: transcription.UtteranceID,
			Text:        decision.Text,
			Canonical:   decision.Canonical,
		}); err != nil {
			return fmt.Errorf("handle query for utterance %d: %w", transcription.UtteranceID, err)
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
			return fmt.Errorf("неизвестная router command %d", decision.Command)
		}
	default:
		return fmt.Errorf("неизвестный router decision %d", decision.Kind)
	}
}

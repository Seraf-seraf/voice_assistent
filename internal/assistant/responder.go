package assistant

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Seraf-seraf/voice_assistent/internal/dialogue"
	"github.com/Seraf-seraf/voice_assistent/internal/llm"
)

var ErrEmptyResponse = errors.New("генератор вернул пустой ответ")

type Response struct {
	UtteranceID uint64
	TurnID      uint64
	Text        string
}

type ResponseHandler func(context.Context, Response) error

type Responder struct {
	dialogue  *dialogue.Manager
	generator llm.Generator
	options   llm.Options
	handler   ResponseHandler
}

func NewResponder(manager *dialogue.Manager, generator llm.Generator, options llm.Options, handler ResponseHandler) (*Responder, error) {
	if manager == nil || generator == nil || isNilDependency(generator) || handler == nil {
		return nil, errors.New("зависимости responder обязательны")
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}
	return &Responder{dialogue: manager, generator: generator, options: options, handler: handler}, nil
}

func (r *Responder) Handle(ctx context.Context, query Query) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	turnID, err := r.dialogue.BeginTurn(query.Text)
	if err != nil {
		return fmt.Errorf("begin dialogue turn: %w", err)
	}
	completed := false
	defer func() {
		if completed {
			return
		}
		if err := r.dialogue.AbortTurn(turnID); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("abort dialogue turn %d: %w", turnID, err))
		}
	}()

	request := llm.Request{Dialogue: r.dialogue.Snapshot(), Options: r.options}
	var responseText strings.Builder
	if err := r.generator.Generate(ctx, request, func(delta llm.TextDelta) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, _ = responseText.WriteString(delta.Text)
		return nil
	}); err != nil {
		return fmt.Errorf("generate response for dialogue turn %d: %w", turnID, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	text := strings.TrimSpace(responseText.String())
	if text == "" {
		return ErrEmptyResponse
	}
	if err := r.handler(ctx, Response{UtteranceID: query.UtteranceID, TurnID: turnID, Text: text}); err != nil {
		return fmt.Errorf("handle response for dialogue turn %d: %w", turnID, err)
	}
	if err := r.dialogue.CompleteTurn(turnID, text); err != nil {
		return fmt.Errorf("complete dialogue turn %d: %w", turnID, err)
	}
	completed = true
	return nil
}

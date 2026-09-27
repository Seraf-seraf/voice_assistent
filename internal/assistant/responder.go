package assistant

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Seraf-seraf/voice_assistent/internal/dialogue"
	"github.com/Seraf-seraf/voice_assistent/internal/llm"
)

var ErrEmptyResponse = errors.New("генератор вернул пустой ответ")

type Responder struct {
	dialogue  *dialogue.Manager
	generator llm.Generator
	options   llm.Options
	sink      ResponseSink
}

func NewResponder(manager *dialogue.Manager, generator llm.Generator, options llm.Options, sink ResponseSink) (*Responder, error) {
	if manager == nil || generator == nil || isNilDependency(generator) || sink == nil || isNilDependency(sink) {
		return nil, errors.New("зависимости обработчика ответа обязательны")
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}
	return &Responder{dialogue: manager, generator: generator, options: options, sink: sink}, nil
}

func (r *Responder) Handle(ctx context.Context, query Query) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	turnID, err := r.dialogue.BeginTurn(query.Text)
	if err != nil {
		return fmt.Errorf("начать ход диалога: %w", err)
	}
	turnCompleted := false
	sinkCompleted := false
	defer func() {
		var cleanupErrors []error
		if !sinkCompleted {
			if err := r.sink.Abort(context.WithoutCancel(ctx), ResponseAbort{UtteranceID: query.UtteranceID, TurnID: turnID}); err != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("отменить вывод ответа для хода диалога %d: %w", turnID, err))
			}
		}
		if !turnCompleted {
			if err := r.dialogue.AbortTurn(turnID); err != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("отменить ход диалога %d: %w", turnID, err))
			}
		}
		if len(cleanupErrors) != 0 {
			resultErr = errors.Join(append([]error{resultErr}, cleanupErrors...)...)
		}
	}()

	request := llm.Request{Dialogue: r.dialogue.Snapshot(), Options: r.options}
	var responseText strings.Builder
	var whitespace responseWhitespace
	if err := r.generator.Generate(ctx, request, func(delta llm.TextDelta) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		visible := whitespace.Push(delta.Text)
		if visible == "" {
			return nil
		}
		if err := r.sink.Push(ctx, ResponseDelta{UtteranceID: query.UtteranceID, TurnID: turnID, Text: visible}); err != nil {
			return fmt.Errorf("передать дельту ответа для хода диалога %d: %w", turnID, err)
		}
		_, _ = responseText.WriteString(visible)
		return nil
	}); err != nil {
		return fmt.Errorf("сгенерировать ответ для хода диалога %d: %w", turnID, err)
	}
	whitespace.Finish()
	text := responseText.String()
	if text == "" {
		return ErrEmptyResponse
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := r.sink.Complete(ctx, Response{UtteranceID: query.UtteranceID, TurnID: turnID, Text: text}); err != nil {
		return fmt.Errorf("завершить вывод ответа для хода диалога %d: %w", turnID, err)
	}
	sinkCompleted = true
	if err := r.dialogue.CompleteTurn(turnID, text); err != nil {
		return fmt.Errorf("завершить ход диалога %d: %w", turnID, err)
	}
	turnCompleted = true
	return nil
}

type responseWhitespace struct {
	started bool
	pending strings.Builder
}

func (w *responseWhitespace) Push(text string) string {
	var visible strings.Builder
	for len(text) > 0 {
		r, size := utf8.DecodeRuneInString(text)
		piece := text[:size]
		text = text[size:]
		if unicode.IsSpace(r) {
			if w.started {
				_, _ = w.pending.WriteString(piece)
			}
			continue
		}
		if w.started && w.pending.Len() != 0 {
			_, _ = visible.WriteString(w.pending.String())
			w.pending.Reset()
		}
		w.started = true
		_, _ = visible.WriteString(piece)
	}
	return visible.String()
}

func (w *responseWhitespace) Finish() {
	w.pending.Reset()
}

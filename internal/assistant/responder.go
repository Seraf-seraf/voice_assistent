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
	var responseText strings.Builder
	defer func() {
		var cleanupErrors []error
		var progress ResponseAbortResult
		validProgress := true
		if !sinkCompleted {
			abort := ResponseAbort{UtteranceID: query.UtteranceID, TurnID: turnID}
			var abortErr error
			progress, abortErr = r.sink.Abort(context.WithoutCancel(ctx), abort)
			validProgress = progress.UtteranceID == abort.UtteranceID && progress.TurnID == abort.TurnID &&
				utf8.ValidString(progress.PlayedText) && strings.HasPrefix(responseText.String(), progress.PlayedText)
			if !validProgress {
				cleanupErrors = append(cleanupErrors, ErrInvalidResponseProgress)
			}
			if abortErr != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("отменить вывод ответа для хода диалога %d: %w", turnID, abortErr))
			}
		}
		interrupted := !sinkCompleted && context.Cause(ctx) == ErrSpeechInterrupted
		if interrupted && validProgress {
			playedText := strings.TrimSpace(progress.PlayedText)
			recordFailed := false
			if playedText != "" {
				if err := r.dialogue.RecordSpoken(turnID, playedText); err != nil {
					recordFailed = true
					cleanupErrors = append(cleanupErrors, fmt.Errorf("записать воспроизведённый префикс хода диалога %d: %w", turnID, err))
				}
			}
			if !recordFailed {
				if err := r.dialogue.InterruptTurn(turnID); err != nil {
					cleanupErrors = append(cleanupErrors, fmt.Errorf("прервать ход диалога %d: %w", turnID, err))
				} else {
					turnCompleted = true
				}
			}
		} else if !turnCompleted {
			if err := r.dialogue.AbortTurn(turnID); err != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("отменить ход диалога %d: %w", turnID, err))
			} else {
				turnCompleted = true
			}
		}
		if interrupted {
			cleanupErrors = append(cleanupErrors, ErrSpeechInterrupted)
		}
		if !turnCompleted {
			if err := r.dialogue.AbortTurn(turnID); err != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("освободить ход диалога %d после ошибки очистки: %w", turnID, err))
			} else {
				turnCompleted = true
			}
		}
		if len(cleanupErrors) != 0 {
			resultErr = errors.Join(append([]error{resultErr}, cleanupErrors...)...)
		}
	}()

	request := llm.Request{Dialogue: r.dialogue.Snapshot(), Options: r.options}
	var whitespace responseWhitespace
	var emitErr error
	generateErr := r.generator.Generate(ctx, request, func(delta llm.TextDelta) error {
		if emitErr != nil {
			return emitErr
		}
		if err := ctx.Err(); err != nil {
			emitErr = err
			return err
		}
		visible := whitespace.Push(delta.Text)
		if visible == "" {
			return nil
		}
		_, _ = responseText.WriteString(visible)
		if err := r.sink.Push(ctx, ResponseDelta{UtteranceID: query.UtteranceID, TurnID: turnID, Text: visible}); err != nil {
			emitErr = fmt.Errorf("передать дельту ответа для хода диалога %d: %w", turnID, err)
			return emitErr
		}
		return nil
	})
	if generateErr != nil || emitErr != nil {
		var failures []error
		if generateErr != nil {
			failures = append(failures, fmt.Errorf("сгенерировать ответ для хода диалога %d: %w", turnID, generateErr))
		}
		if emitErr != nil && (generateErr == nil || !errors.Is(generateErr, emitErr)) {
			failures = append(failures, emitErr)
		}
		return errors.Join(failures...)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	whitespace.Finish()
	text := responseText.String()
	if text == "" {
		return ErrEmptyResponse
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

package assistant

import (
	"context"
	"errors"
)

type Response struct {
	UtteranceID uint64
	TurnID      uint64
	Text        string
}

type ResponseDelta struct {
	UtteranceID uint64
	TurnID      uint64
	Text        string
}

type ResponseAbort struct {
	UtteranceID uint64
	TurnID      uint64
}

type ResponseAbortResult struct {
	UtteranceID uint64
	TurnID      uint64
	PlayedText  string
}

var ErrInvalidResponseProgress = errors.New("приёмник ответа вернул недостоверный прогресс воспроизведения")

type ResponseSink interface {
	Push(context.Context, ResponseDelta) error
	Complete(context.Context, Response) error
	Abort(context.Context, ResponseAbort) (ResponseAbortResult, error)
}

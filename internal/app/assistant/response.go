package assistant

import "context"

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

type ResponseSink interface {
	Push(context.Context, ResponseDelta) error
	Complete(context.Context, Response) error
	Abort(context.Context, ResponseAbort) error
}

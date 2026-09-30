// Package diagnostics передаёт безопасные технические события между слоями приложения.
package diagnostics

import (
	"context"
	"time"
)

type Event struct {
	Phase          string
	UtteranceID    uint64
	TurnID         uint64
	At             time.Time
	Duration       time.Duration
	SinceSpeechEnd time.Duration
	Runes          int
	Frames         int
	SampleRate     int
}

type Observer func(Event)

type contextKey struct{}

type contextValues struct {
	observer      Observer
	utteranceID   uint64
	turnID        uint64
	speechEndedAt time.Time
}

func WithObserver(ctx context.Context, observer Observer) context.Context {
	values := valuesFrom(ctx)
	values.observer = observer
	return context.WithValue(ctx, contextKey{}, values)
}

func WithIDs(ctx context.Context, utteranceID, turnID uint64) context.Context {
	values := valuesFrom(ctx)
	values.utteranceID = utteranceID
	values.turnID = turnID
	return context.WithValue(ctx, contextKey{}, values)
}

func WithSpeechEnd(ctx context.Context, at time.Time) context.Context {
	values := valuesFrom(ctx)
	values.speechEndedAt = at
	return context.WithValue(ctx, contextKey{}, values)
}

func Emit(ctx context.Context, event Event) {
	values, ok := ctx.Value(contextKey{}).(contextValues)
	if !ok || values.observer == nil {
		return
	}
	if event.UtteranceID == 0 {
		event.UtteranceID = values.utteranceID
	}
	if event.TurnID == 0 {
		event.TurnID = values.turnID
	}
	if event.At.IsZero() {
		event.At = time.Now()
	}
	if !values.speechEndedAt.IsZero() {
		event.SinceSpeechEnd = event.At.Sub(values.speechEndedAt)
	}
	values.observer(event)
}

func valuesFrom(ctx context.Context) contextValues {
	values, _ := ctx.Value(contextKey{}).(contextValues)
	return values
}

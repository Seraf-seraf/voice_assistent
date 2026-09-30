package diagnostics

import (
	"context"
	"testing"
	"time"
)

func TestEmitPropagatesObserverAndTurnIDs(t *testing.T) {
	var got Event
	ctx := WithObserver(context.Background(), func(event Event) { got = event })
	ctx = WithIDs(ctx, 17, 29)
	Emit(ctx, Event{Phase: "llm_first_delta"})

	if got.Phase != "llm_first_delta" || got.UtteranceID != 17 || got.TurnID != 29 || got.At.IsZero() {
		t.Fatalf("observed event = %+v", got)
	}
}

func TestEmitIsNoopWithoutObserver(t *testing.T) {
	Emit(context.Background(), Event{Phase: "llm_first_delta"})
}

func TestEmitIncludesElapsedTimeSinceSpeechEnd(t *testing.T) {
	var got Event
	endedAt := time.Now().Add(-time.Second)
	ctx := WithObserver(context.Background(), func(event Event) { got = event })
	ctx = WithSpeechEnd(ctx, endedAt)
	Emit(ctx, Event{Phase: "output_started"})

	if got.SinceSpeechEnd < time.Second {
		t.Fatalf("elapsed time since speech end = %s, want at least 1s", got.SinceSpeechEnd)
	}
}

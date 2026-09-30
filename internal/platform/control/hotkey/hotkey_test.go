package hotkey

import (
	"testing"
	"time"
)

func TestKeyTrackerPublishesOnlyTransitions(t *testing.T) {
	queue := newStateQueue()
	tracker := keyTracker{queue: queue}
	pressedAt := time.Unix(100, 0)
	releasedAt := pressedAt.Add(time.Second)

	tracker.transition(true, pressedAt)
	tracker.transition(true, pressedAt.Add(time.Millisecond))
	state := receiveState(t, queue.states)
	if !state.Pressed || !state.At.Equal(pressedAt) {
		t.Fatalf("pressed state = %+v", state)
	}
	assertNoState(t, queue.states)

	tracker.transition(false, releasedAt)
	state = receiveState(t, queue.states)
	if state.Pressed || !state.At.Equal(releasedAt) {
		t.Fatalf("released state = %+v", state)
	}
}

func TestStateQueueKeepsLatestState(t *testing.T) {
	queue := newStateQueue()
	queue.publish(State{Pressed: true, At: time.Unix(100, 0)})
	latest := State{Pressed: false, At: time.Unix(101, 0)}
	queue.publish(latest)

	if state := receiveState(t, queue.states); state != latest {
		t.Fatalf("state = %+v, want %+v", state, latest)
	}
	assertNoState(t, queue.states)
}

func TestNormalizeKey(t *testing.T) {
	key, err := normalizeKey(" F8 ")
	if err != nil {
		t.Fatalf("normalizeKey() error: %v", err)
	}
	if key != "f8" {
		t.Fatalf("normalizeKey() = %q, want f8", key)
	}
	if _, err := normalizeKey("  "); err == nil {
		t.Fatal("normalizeKey() accepted empty key")
	}
}

func receiveState(t *testing.T, states <-chan State) State {
	t.Helper()
	select {
	case state := <-states:
		return state
	case <-time.After(time.Second):
		t.Fatal("state not received")
		return State{}
	}
}

func assertNoState(t *testing.T, states <-chan State) {
	t.Helper()
	select {
	case state := <-states:
		t.Fatalf("unexpected state: %+v", state)
	default:
	}
}

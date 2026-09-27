package hotkey

import (
	"context"
	"errors"
	"strings"
	"time"
)

var (
	ErrUnavailable = errors.New("глобальная клавиша доступна только в сборке Windows с CGO")
	ErrClosed      = errors.New("глобальная клавиша закрыта")
	ErrRunning     = errors.New("глобальная клавиша уже запущена")
	ErrInUse       = errors.New("другая глобальная клавиша уже запущена")
)

type State struct {
	Pressed bool
	At      time.Time
}

type Hook interface {
	Run(ctx context.Context) error
	States() <-chan State
	Close() error
}

type stateQueue struct {
	states chan State
}

func newStateQueue() *stateQueue {
	return &stateQueue{states: make(chan State, 1)}
}

func (q *stateQueue) publish(state State) {
	select {
	case q.states <- state:
		return
	default:
	}
	select {
	case <-q.states:
	default:
	}
	q.states <- state
}

type keyTracker struct {
	queue   *stateQueue
	pressed bool
}

func (t *keyTracker) transition(pressed bool, at time.Time) {
	if t.pressed == pressed {
		return
	}
	if at.IsZero() {
		at = time.Now()
	}
	t.pressed = pressed
	t.queue.publish(State{Pressed: pressed, At: at})
}

func normalizeKey(key string) (string, error) {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return "", errors.New("клавиша PTT обязательна")
	}
	return key, nil
}

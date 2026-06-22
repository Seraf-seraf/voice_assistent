//go:build windows && cgo

package hotkey

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	hooklib "github.com/robotn/gohook"
)

const (
	hookCreated uint32 = iota
	hookRunning
	hookClosed
)

var globalActive atomic.Bool

type globalHook struct {
	keyCode uint16
	queue   *stateQueue
	tracker keyTracker

	state      atomic.Uint32
	stop       chan struct{}
	done       chan struct{}
	stopOnce   sync.Once
	finishOnce sync.Once
}

// NewGlobal создаёт пассивный Windows keyboard hook для одной hold-клавиши.
func NewGlobal(key string) (Hook, error) {
	key, err := normalizeKey(key)
	if err != nil {
		return nil, err
	}
	keyCode, ok := hooklib.Keycode[key]
	if !ok || keyCode == 0 {
		return nil, fmt.Errorf("неизвестная клавиша PTT %q", key)
	}
	queue := newStateQueue()
	return &globalHook{
		keyCode: keyCode,
		queue:   queue,
		tracker: keyTracker{queue: queue},
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}, nil
}

func (h *globalHook) Run(ctx context.Context) error {
	if !h.state.CompareAndSwap(hookCreated, hookRunning) {
		if h.state.Load() == hookRunning {
			return ErrRunning
		}
		return ErrClosed
	}
	defer h.finish()
	if !globalActive.CompareAndSwap(false, true) {
		return ErrInUse
	}
	defer globalActive.Store(false)

	rawEvents := hooklib.Start(10)
	defer hooklib.End()
	if err := waitUntilEnabled(ctx, h.stop, rawEvents); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			h.tracker.transition(false, time.Now())
			return nil
		case <-h.stop:
			h.tracker.transition(false, time.Now())
			return nil
		case event, open := <-rawEvents:
			if !open {
				return errors.New("канал Windows keyboard hook неожиданно закрыт")
			}
			h.processEvent(event)
		}
	}
}

func (h *globalHook) States() <-chan State {
	return h.queue.states
}

func (h *globalHook) Close() error {
	h.stopOnce.Do(func() { close(h.stop) })
	if h.state.CompareAndSwap(hookCreated, hookClosed) {
		h.finish()
	}
	<-h.done
	return nil
}

func (h *globalHook) processEvent(event hooklib.Event) {
	if event.Keycode != h.keyCode {
		return
	}
	switch event.Kind {
	case hooklib.KeyDown:
		h.tracker.transition(true, event.When)
	case hooklib.KeyUp:
		h.tracker.transition(false, event.When)
	}
}

func (h *globalHook) finish() {
	h.finishOnce.Do(func() {
		h.state.Store(hookClosed)
		close(h.queue.states)
		close(h.done)
	})
}

func waitUntilEnabled(ctx context.Context, stop <-chan struct{}, events <-chan hooklib.Event) error {
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-stop:
			return nil
		case <-timer.C:
			return errors.New("Windows keyboard hook не запустился за 2s")
		case event, open := <-events:
			if !open {
				return errors.New("канал Windows keyboard hook закрыт при запуске")
			}
			if event.Kind == hooklib.HookEnabled {
				return nil
			}
		}
	}
}

//go:build windows && amd64 && cgo

package wasapi

/*
#include <stdlib.h>
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/Seraf-seraf/voice_assistent/internal/service/diagnostics"
	"github.com/gen2brain/malgo"
)

type runState struct {
	session      *session
	stopped      chan struct{}
	stopExpected atomic.Bool
	stopOnce     sync.Once
}

type wasapiDevice struct {
	context    *malgo.AllocatedContext
	device     *malgo.Device
	sampleRate int
	active     atomic.Pointer[runState]
	closed     bool
}

var _ nativeDevice = (*wasapiDevice)(nil)

func Open(ctx context.Context, options Options) (*Player, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	backends := []malgo.Backend{malgo.BackendWasapi}
	audioContext, err := malgo.InitContext(backends, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("создать контекст WASAPI: %w", err)
	}
	native := &wasapiDevice{context: audioContext, sampleRate: options.SampleRate}
	deviceConfig := malgo.DefaultDeviceConfig(malgo.Playback)
	deviceConfig.Playback.Format = malgo.FormatF32
	deviceConfig.Playback.Channels = 1
	deviceConfig.SampleRate = uint32(options.SampleRate)
	devicePointer, err := resolvePlaybackDevice(audioContext, options.Device)
	if err != nil {
		return nil, errors.Join(err, native.Close())
	}
	if devicePointer != nil {
		deviceConfig.Playback.DeviceID = devicePointer
		defer C.free(devicePointer)
	}
	device, err := malgo.InitDevice(audioContext.Context, deviceConfig, malgo.DeviceCallbacks{
		Data: func(output []byte, _ []byte, frames uint32) {
			if active := native.active.Load(); active != nil {
				active.session.render(output, frames)
				return
			}
			clear(output)
		},
		Stop: func() {
			if active := native.active.Load(); active != nil && !active.stopExpected.Load() {
				active.stopOnce.Do(func() { close(active.stopped) })
			}
		},
	})
	if err != nil {
		return nil, errors.Join(fmt.Errorf("открыть выбранный WASAPI-выход: %w", err), native.Close())
	}
	native.device = device
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(err, native.Close())
	}
	diagnostics.Emit(ctx, diagnostics.Event{Phase: "output_configured", SampleRate: options.SampleRate})
	return newPlayer(native, options.SampleRate), nil
}

func resolvePlaybackDevice(audioContext *malgo.AllocatedContext, selector string) (unsafe.Pointer, error) {
	if selector == "" || selector == "default" {
		return nil, nil
	}
	devices, err := audioContext.Devices(malgo.Playback)
	if err != nil {
		return nil, fmt.Errorf("получить список выходов WASAPI: %w", err)
	}
	var selected *malgo.DeviceID
	for index := range devices {
		if devices[index].Name() != selector {
			continue
		}
		if selected != nil {
			return nil, fmt.Errorf("имя выхода WASAPI %q неоднозначно", selector)
		}
		selected = &devices[index].ID
	}
	if selected == nil {
		return nil, fmt.Errorf("выход WASAPI %q не найден", selector)
	}
	return selected.Pointer(), nil
}

func (d *wasapiDevice) Play(ctx context.Context, samples []float32) error {
	if d.closed || d.device == nil {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	active := &runState{session: newSession(samples), stopped: make(chan struct{})}
	d.active.Store(active)
	if err := d.device.Start(); err != nil {
		active.stopExpected.Store(true)
		stopErr := d.device.Stop()
		d.active.CompareAndSwap(active, nil)
		return errors.Join(fmt.Errorf("запустить вывод WASAPI: %w", err), stopErr)
	}
	diagnostics.Emit(ctx, diagnostics.Event{Phase: "output_started", Frames: len(samples), SampleRate: d.sampleRate})
	select {
	case <-active.session.finished:
		active.stopExpected.Store(true)
		if err := d.device.Stop(); err != nil {
			d.active.CompareAndSwap(active, nil)
			return fmt.Errorf("остановить WASAPI после воспроизведения: %w", err)
		}
		d.active.CompareAndSwap(active, nil)
		return nil
	case <-ctx.Done():
		active.session.cancel()
		active.stopExpected.Store(true)
		stopErr := d.device.Stop()
		d.active.CompareAndSwap(active, nil)
		if stopErr != nil {
			return errors.Join(ctx.Err(), fmt.Errorf("сбросить текущий вывод WASAPI: %w", stopErr))
		}
		return ctx.Err()
	case <-active.stopped:
		active.session.cancel()
		active.stopExpected.Store(true)
		stopErr := d.device.Stop()
		d.active.CompareAndSwap(active, nil)
		return errors.Join(ErrDevice, errors.New("WASAPI неожиданно остановил устройство"), stopErr)
	}
}

func (d *wasapiDevice) Close() error {
	if d.closed {
		return nil
	}
	d.closed = true
	var resultErr error
	if d.device != nil {
		if d.device.IsStarted() {
			if err := d.device.Stop(); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("остановить WASAPI: %w", err))
			}
		}
		d.device.Uninit()
		d.device = nil
	}
	if d.context == nil {
		return resultErr
	}
	if err := d.context.Uninit(); err != nil {
		resultErr = errors.Join(resultErr, fmt.Errorf("закрыть контекст WASAPI: %w", err))
	}
	d.context.Free()
	d.context = nil
	return resultErr
}

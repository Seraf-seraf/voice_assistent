//go:build linux && amd64 && cgo && !android && !musl

package alsa

/*
#cgo pkg-config: alsa
#include <alsa/asoundlib.h>
#include <errno.h>
#include <stdlib.h>
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

type nativePCMDevice struct {
	handle *C.snd_pcm_t
}

func Open(ctx context.Context, options Options) (*Player, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}
	device, err := openNativePCMDevice(options)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(err, device.Close())
	}
	return newPlayer(device, options.SampleRate), nil
}

func openNativePCMDevice(options Options) (*nativePCMDevice, error) {
	deviceName := options.Device
	if deviceName == "" {
		deviceName = "default"
	}
	cName := C.CString(deviceName)
	defer C.free(unsafe.Pointer(cName))

	var handle *C.snd_pcm_t
	if result := C.snd_pcm_open(&handle, cName, C.SND_PCM_STREAM_PLAYBACK, C.SND_PCM_NONBLOCK); result < 0 {
		openErr := fmt.Errorf("%w: открыть PCM: %w", ErrDevice, nativeErr(result))
		if handle != nil {
			if closeResult := C.snd_pcm_close(handle); closeResult < 0 {
				openErr = errors.Join(openErr, fmt.Errorf("%w: закрыть неудачно открытый PCM: %w", ErrDevice, nativeErr(closeResult)))
			}
		}
		return nil, openErr
	}
	device := &nativePCMDevice{handle: handle}
	if result := C.snd_pcm_set_params(
		handle,
		C.SND_PCM_FORMAT_FLOAT_LE,
		C.SND_PCM_ACCESS_RW_INTERLEAVED,
		1,
		C.uint(options.SampleRate),
		1,
		100000,
	); result < 0 {
		return nil, errors.Join(fmt.Errorf("%w: настроить PCM: %w", ErrDevice, nativeErr(result)), device.Close())
	}
	return device, nil
}

func (d *nativePCMDevice) Write(samples []float32) (int, error) {
	if len(samples) == 0 {
		return 0, nil
	}
	frames := C.snd_pcm_uframes_t(len(samples))
	result := C.snd_pcm_writei(d.handle, unsafe.Pointer(&samples[0]), frames)
	runtime.KeepAlive(samples)
	if result < 0 {
		return 0, nativeErr(C.int(result))
	}
	return int(result), nil
}

func (d *nativePCMDevice) Wait(_ context.Context, timeout time.Duration) error {
	if timeout <= 0 {
		return nil
	}
	milliseconds := timeout.Milliseconds()
	if milliseconds < 1 {
		milliseconds = 1
	}
	result := C.snd_pcm_wait(d.handle, C.int(milliseconds))
	if result < 0 {
		return nativeErr(result)
	}
	return nil
}

func (d *nativePCMDevice) Drain() error {
	result := C.snd_pcm_drain(d.handle)
	if result < 0 {
		return nativeErr(result)
	}
	return nil
}

func (d *nativePCMDevice) Drop() error {
	result := C.snd_pcm_drop(d.handle)
	if result < 0 {
		return nativeErr(result)
	}
	return nil
}

func (d *nativePCMDevice) Prepare() error {
	result := C.snd_pcm_prepare(d.handle)
	if result < 0 {
		return nativeErr(result)
	}
	return nil
}

func (d *nativePCMDevice) Close() error {
	if d.handle == nil {
		return nil
	}
	handle := d.handle
	d.handle = nil
	if result := C.snd_pcm_close(handle); result < 0 {
		return fmt.Errorf("%w: закрыть PCM: %w", ErrDevice, nativeErr(result))
	}
	return nil
}

func nativeErr(result C.int) error {
	if result >= 0 {
		return nil
	}
	return syscall.Errno(-result)
}

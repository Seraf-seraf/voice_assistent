//go:build cgo

package input

/*
#include <stdlib.h>
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/gen2brain/malgo"

	"github.com/Seraf-seraf/voice_assistent/internal/audio"
)

const (
	sourceCreated uint32 = iota
	sourceRunning
	sourceClosed
)

type malgoSource struct {
	options MalgoOptions
	framer  *Framer

	state      atomic.Uint32
	stop       chan struct{}
	done       chan struct{}
	stopOnce   sync.Once
	finishOnce sync.Once
}

// NewMalgoSource создаёт microphone source через miniaudio.
// Аудиоустройство открывается только при вызове Run.
func NewMalgoSource(options MalgoOptions) (Source, error) {
	if options.PeriodSizeInMS == 0 {
		options.PeriodSizeInMS = uint32(options.Format.FrameDuration / time.Millisecond)
	}
	framer, err := NewFramer(options.Format, options.QueueSize)
	if err != nil {
		return nil, err
	}
	return &malgoSource{
		options: options,
		framer:  framer,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}, nil
}

// ListCaptureDevices возвращает доступные microphone devices miniaudio.
func ListCaptureDevices() (devices []CaptureDevice, resultErr error) {
	audioContext, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("создать miniaudio context: %w", err)
	}
	defer func() {
		if err := audioContext.Uninit(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("закрыть miniaudio context: %w", err))
		}
		audioContext.Free()
	}()

	infos, err := audioContext.Devices(malgo.Capture)
	if err != nil {
		return nil, fmt.Errorf("получить список микрофонов: %w", err)
	}
	devices = make([]CaptureDevice, 0, len(infos))
	for index := range infos {
		devices = append(devices, CaptureDevice{
			ID: infos[index].ID.String(), Name: infos[index].Name(), IsDefault: infos[index].IsDefault != 0,
		})
	}
	return devices, nil
}

func (s *malgoSource) Run(ctx context.Context) (resultErr error) {
	if !s.state.CompareAndSwap(sourceCreated, sourceRunning) {
		if s.state.Load() == sourceRunning {
			return ErrSourceRunning
		}
		return ErrSourceClosed
	}
	defer s.finish()

	audioContext, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return fmt.Errorf("создать miniaudio context: %w", err)
	}
	var device *malgo.Device
	defer func() {
		if device != nil {
			device.Uninit()
		}
		if err := audioContext.Uninit(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("закрыть miniaudio context: %w", err))
		}
		audioContext.Free()
	}()

	deviceConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	deviceConfig.Capture.Format = malgo.FormatS16
	deviceConfig.Capture.Channels = uint32(s.options.Format.Channels)
	deviceConfig.SampleRate = uint32(s.options.Format.SampleRate)
	deviceConfig.PeriodSizeInMilliseconds = s.options.PeriodSizeInMS

	deviceIDPointer, err := resolveCaptureDevice(audioContext, s.options.CaptureDevice)
	if err != nil {
		return err
	}
	if deviceIDPointer != nil {
		deviceConfig.Capture.DeviceID = deviceIDPointer
		defer C.free(deviceIDPointer)
	}

	callbackErrors := make(chan error, 1)
	stopped := make(chan struct{})
	var stoppedOnce sync.Once
	callbacks := malgo.DeviceCallbacks{
		Data: func(_ []byte, inputSamples []byte, frameCount uint32) {
			if err := s.writeInput(inputSamples, frameCount, time.Now()); err != nil && !errors.Is(err, ErrClosed) {
				select {
				case callbackErrors <- err:
				default:
				}
			}
		},
		Stop: func() {
			stoppedOnce.Do(func() { close(stopped) })
		},
	}
	device, err = malgo.InitDevice(audioContext.Context, deviceConfig, callbacks)
	if err != nil {
		return fmt.Errorf("открыть микрофон: %w", err)
	}
	if err := device.Start(); err != nil {
		return fmt.Errorf("запустить запись с микрофона: %w", err)
	}

	select {
	case <-ctx.Done():
		return nil
	case <-s.stop:
		return nil
	case err := <-callbackErrors:
		return fmt.Errorf("обработать входной audio block: %w", err)
	case <-stopped:
		return errors.New("аудиоустройство неожиданно остановлено")
	}
}

func (s *malgoSource) Frames() <-chan audio.Frame {
	return s.framer.Frames()
}

func (s *malgoSource) DroppedFrames() uint64 {
	return s.framer.DroppedFrames()
}

func (s *malgoSource) Close() error {
	s.stopOnce.Do(func() { close(s.stop) })
	if s.state.CompareAndSwap(sourceCreated, sourceClosed) {
		s.finish()
	}
	<-s.done
	return nil
}

func (s *malgoSource) finish() {
	s.finishOnce.Do(func() {
		s.state.Store(sourceClosed)
		_ = s.framer.Close()
		close(s.done)
	})
}

func (s *malgoSource) writeInput(inputSamples []byte, frameCount uint32, capturedAt time.Time) error {
	if len(inputSamples) == 0 {
		return nil
	}
	duration := time.Duration(frameCount) * time.Second / time.Duration(s.options.Format.SampleRate)
	return s.framer.WritePCM(inputSamples, capturedAt.Add(-duration))
}

func resolveCaptureDevice(audioContext *malgo.AllocatedContext, selector string) (deviceIDPointer unsafe.Pointer, err error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return nil, nil
	}
	devices, err := audioContext.Devices(malgo.Capture)
	if err != nil {
		return nil, fmt.Errorf("получить список микрофонов: %w", err)
	}
	for index := range devices {
		if devices[index].ID.String() == selector || strings.EqualFold(devices[index].Name(), selector) {
			return devices[index].ID.Pointer(), nil
		}
	}
	return nil, fmt.Errorf("микрофон %q не найден", selector)
}

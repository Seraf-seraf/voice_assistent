package alsa

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/audio"
	"github.com/Seraf-seraf/voice_assistent/internal/audio/output"
)

const (
	maxSampleRate        = 192000
	deviceWaitInterval   = 20 * time.Millisecond
	minimumPrefillFrames = 2
	prefillDuration      = 100 * time.Millisecond
)

var (
	ErrInvalidOptions      = errors.New("некорректные параметры вывода ALSA")
	ErrClosed              = errors.New("вывод ALSA закрыт")
	ErrDevice              = errors.New("ошибка устройства ALSA")
	ErrUnsupportedPlatform = output.ErrUnsupportedPlatform
)

type Options struct {
	Device     string
	SampleRate int
}

func (o Options) Validate() error {
	if strings.ContainsRune(o.Device, '\x00') {
		return fmt.Errorf("%w: имя устройства не должно содержать NUL", ErrInvalidOptions)
	}
	if o.SampleRate <= 0 || o.SampleRate > maxSampleRate {
		return fmt.Errorf("%w: частота дискретизации должна быть в диапазоне [1, %d]", ErrInvalidOptions, maxSampleRate)
	}
	return nil
}

type pcmDevice interface {
	Write([]float32) (int, error)
	Wait(context.Context, time.Duration) error
	PeriodFrames() int
	PrefillFrames() int
	Start() error
	Drain() error
	Drop() error
	Prepare() error
	Close() error
}

var _ output.Player = (*Player)(nil)

type Player struct {
	device     pcmDevice
	sampleRate int
	closed     atomic.Bool
}

func newPlayer(device pcmDevice, sampleRate int) *Player {
	return &Player{device: device, sampleRate: sampleRate}
}

func (p *Player) Play(ctx context.Context, pcm audio.PCM) error {
	if p.closed.Load() {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := pcm.Validate(); err != nil {
		return fmt.Errorf("проверить PCM: %w", err)
	}
	if pcm.SampleRate != p.sampleRate {
		return fmt.Errorf("частота дискретизации PCM %d не совпадает с частотой ALSA %d", pcm.SampleRate, p.sampleRate)
	}
	if err := p.device.Prepare(); err != nil {
		return fmt.Errorf("подготовить ALSA PCM: %w", err)
	}
	prepared := true
	fail := func(cause error) error {
		if !prepared {
			return cause
		}
		if dropErr := p.device.Drop(); dropErr != nil {
			return errors.Join(cause, fmt.Errorf("сбросить ALSA PCM: %w", dropErr))
		}
		return cause
	}

	chunkSize := p.device.PeriodFrames()
	prefillFrames := p.device.PrefillFrames()
	if chunkSize < 1 || prefillFrames < 1 {
		return fail(fmt.Errorf("%w: ALSA вернула недопустимые размеры периода или предварительного буфера", ErrDevice))
	}
	if prefillFrames > len(pcm.Samples) {
		prefillFrames = len(pcm.Samples)
	}
	started := false
	for offset := 0; offset < len(pcm.Samples); {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		writeLimit := len(pcm.Samples)
		if !started {
			writeLimit = prefillFrames
		}
		end := writeLimit
		if chunkSize < writeLimit-offset {
			end = offset + chunkSize
		}
		if end > writeLimit {
			end = writeLimit
		}
		if end <= offset {
			return fail(fmt.Errorf("%w: ALSA не достигла порога предварительного заполнения", ErrDevice))
		}
		chunk := pcm.Samples[offset:end]
		written, writeErr := p.device.Write(chunk)
		if written < 0 || written > len(chunk) {
			return fail(fmt.Errorf("запись ALSA вернула недопустимое число кадров %d для блока %d", written, len(chunk)))
		}
		if written > 0 {
			offset += written
		}
		retryableWriteErr := errors.Is(writeErr, syscall.EAGAIN) || errors.Is(writeErr, syscall.EINTR)
		if !started && offset >= prefillFrames && written > 0 && (writeErr == nil || retryableWriteErr) {
			if err := ctx.Err(); err != nil {
				return fail(err)
			}
			if err := p.device.Start(); err != nil {
				return fail(fmt.Errorf("запустить ALSA PCM: %w", err))
			}
			started = true
		}
		if writeErr == nil && written > 0 {
			continue
		}
		if errors.Is(writeErr, syscall.EINTR) {
			if err := ctx.Err(); err != nil {
				return fail(errors.Join(writeErr, err))
			}
			continue
		}
		if writeErr != nil && !errors.Is(writeErr, syscall.EAGAIN) {
			return fail(fmt.Errorf("записать ALSA PCM: %w", writeErr))
		}
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		if err := p.device.Wait(ctx, deviceWaitInterval); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return fail(errors.Join(err, ctxErr))
			}
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			return fail(fmt.Errorf("ожидать готовность ALSA PCM: %w", err))
		}
	}
	if !started {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		if err := p.device.Start(); err != nil {
			return fail(fmt.Errorf("запустить ALSA PCM: %w", err))
		}
	}

	for {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		err := p.device.Drain()
		if err == nil {
			prepared = false
			return nil
		}
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if !errors.Is(err, syscall.EAGAIN) {
			return fail(fmt.Errorf("дренировать ALSA PCM: %w", err))
		}
		timer := time.NewTimer(deviceWaitInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fail(ctx.Err())
		case <-timer.C:
		}
	}
}

func (p *Player) Close() error {
	if !p.closed.CompareAndSwap(false, true) {
		return nil
	}
	return p.device.Close()
}

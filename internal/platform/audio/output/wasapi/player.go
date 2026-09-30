package wasapi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/service/audio"
	"github.com/Seraf-seraf/voice_assistent/internal/service/audio/output"
	"github.com/Seraf-seraf/voice_assistent/internal/service/diagnostics"
)

const maxSampleRate = 192000

var (
	ErrInvalidOptions      = errors.New("некорректные параметры WASAPI")
	ErrClosed              = errors.New("вывод WASAPI закрыт")
	ErrDevice              = errors.New("ошибка вывода WASAPI")
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

type nativeDevice interface {
	Play(context.Context, []float32) error
	Close() error
}

var _ output.Player = (*Player)(nil)

type Player struct {
	mu         sync.Mutex
	device     nativeDevice
	sampleRate int
	closed     bool
}

func newPlayer(device nativeDevice, sampleRate int) *Player {
	return &Player{device: device, sampleRate: sampleRate}
}

func (p *Player) Play(ctx context.Context, pcm audio.PCM) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	startedAt := time.Now()
	diagnostics.Emit(ctx, diagnostics.Event{Phase: "output_play_requested", Frames: len(pcm.Samples), SampleRate: pcm.SampleRate})
	if err := pcm.Validate(); err != nil {
		return fmt.Errorf("проверить PCM: %w", err)
	}
	if pcm.SampleRate != p.sampleRate {
		return fmt.Errorf("частота PCM %d не совпадает с частотой вывода %d", pcm.SampleRate, p.sampleRate)
	}
	if err := p.device.Play(ctx, pcm.Samples); err != nil {
		diagnostics.Emit(ctx, diagnostics.Event{Phase: "output_play_failed", Duration: time.Since(startedAt), Frames: len(pcm.Samples), SampleRate: pcm.SampleRate})
		return err
	}
	diagnostics.Emit(ctx, diagnostics.Event{Phase: "output_drained", Duration: time.Since(startedAt), Frames: len(pcm.Samples), SampleRate: pcm.SampleRate})
	return nil
}

func (p *Player) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	if err := p.device.Close(); err != nil {
		return fmt.Errorf("закрыть вывод WASAPI: %w", err)
	}
	return nil
}

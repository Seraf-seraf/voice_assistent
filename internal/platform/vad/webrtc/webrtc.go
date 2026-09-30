package webrtc

import (
	"errors"
	"fmt"
	"time"

	libvad "github.com/rolandhe/go-vad"

	"github.com/Seraf-seraf/voice_assistent/internal/service/audio"
	servicevad "github.com/Seraf-seraf/voice_assistent/internal/service/vad"
)

type webRTCDetector struct {
	format audio.Format
	engine *libvad.VAD
	closed bool
}

// NewWebRTCDetector создаёт stateful WebRTC VAD для канонического аудиопотока.
// Один detector должен вызываться последовательно только из одной goroutine.
func NewWebRTCDetector(format audio.Format, aggressiveness int) (servicevad.Detector, error) {
	if err := validateWebRTCFormat(format); err != nil {
		return nil, err
	}
	if aggressiveness < 0 || aggressiveness > 3 {
		return nil, errors.New("агрессивность WebRTC VAD должна быть от 0 до 3")
	}

	engine := libvad.New()
	if err := engine.SetSampleRate(libvad.SampleRate16k); err != nil {
		return nil, fmt.Errorf("настроить частоту WebRTC VAD: %w", err)
	}
	if err := engine.SetMode(libvad.Mode(aggressiveness)); err != nil {
		return nil, fmt.Errorf("настроить агрессивность WebRTC VAD: %w", err)
	}
	return &webRTCDetector{format: format, engine: engine}, nil
}

func (d *webRTCDetector) Classify(frame audio.Frame) (servicevad.Activity, error) {
	if d.closed {
		return servicevad.Silence, errors.New("WebRTC VAD уже закрыт")
	}
	if err := frame.Validate(d.format); err != nil {
		return servicevad.Silence, fmt.Errorf("проверить аудиокадр: %w", err)
	}

	result, err := d.engine.Process(frame.Samples)
	if err != nil {
		return servicevad.Silence, fmt.Errorf("классифицировать аудиокадр: %w", err)
	}
	if result == libvad.ResultVoice {
		return servicevad.Speech, nil
	}
	return servicevad.Silence, nil
}

func (d *webRTCDetector) Close() error {
	d.closed = true
	return nil
}

func validateWebRTCFormat(format audio.Format) error {
	if err := format.Validate(); err != nil {
		return fmt.Errorf("формат аудио: %w", err)
	}
	if format.SampleRate != 16000 {
		return errors.New("WebRTC VAD требует частоту дискретизации 16000 Гц")
	}
	if format.Channels != 1 {
		return errors.New("WebRTC VAD требует одноканальный звук")
	}
	if format.FrameDuration != 10*time.Millisecond && format.FrameDuration != 20*time.Millisecond && format.FrameDuration != 30*time.Millisecond {
		return errors.New("WebRTC VAD поддерживает кадры длительностью 10, 20 или 30 мс")
	}
	return nil
}

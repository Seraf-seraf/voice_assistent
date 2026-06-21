package vad

import (
	"errors"
	"fmt"
	"time"

	webrtc "github.com/rolandhe/go-vad"

	"github.com/Seraf-seraf/voice_assistent/internal/audio"
)

type webRTCDetector struct {
	format audio.Format
	engine *webrtc.VAD
	closed bool
}

// NewWebRTCDetector создаёт stateful WebRTC VAD для канонического аудиопотока.
// Один detector должен вызываться последовательно только из одной goroutine.
func NewWebRTCDetector(format audio.Format, aggressiveness int) (Detector, error) {
	if err := validateWebRTCFormat(format); err != nil {
		return nil, err
	}
	if aggressiveness < 0 || aggressiveness > 3 {
		return nil, errors.New("агрессивность WebRTC VAD должна быть от 0 до 3")
	}

	engine := webrtc.New()
	if err := engine.SetSampleRate(webrtc.SampleRate16k); err != nil {
		return nil, fmt.Errorf("настроить частоту WebRTC VAD: %w", err)
	}
	if err := engine.SetMode(webrtc.Mode(aggressiveness)); err != nil {
		return nil, fmt.Errorf("настроить агрессивность WebRTC VAD: %w", err)
	}
	return &webRTCDetector{format: format, engine: engine}, nil
}

func (d *webRTCDetector) Classify(frame audio.Frame) (Activity, error) {
	if d.closed {
		return Silence, errors.New("WebRTC VAD уже закрыт")
	}
	if err := frame.Validate(d.format); err != nil {
		return Silence, fmt.Errorf("проверить audio frame: %w", err)
	}

	result, err := d.engine.Process(frame.Samples)
	if err != nil {
		return Silence, fmt.Errorf("классифицировать audio frame: %w", err)
	}
	if result == webrtc.ResultVoice {
		return Speech, nil
	}
	return Silence, nil
}

func (d *webRTCDetector) Close() error {
	d.closed = true
	return nil
}

func validateWebRTCFormat(format audio.Format) error {
	if err := format.Validate(); err != nil {
		return fmt.Errorf("audio format: %w", err)
	}
	if format.SampleRate != 16000 {
		return errors.New("WebRTC VAD требует sample rate 16000 Hz")
	}
	if format.Channels != 1 {
		return errors.New("WebRTC VAD требует mono audio")
	}
	if format.FrameDuration != 10*time.Millisecond && format.FrameDuration != 20*time.Millisecond && format.FrameDuration != 30*time.Millisecond {
		return errors.New("WebRTC VAD поддерживает frames длительностью 10, 20 или 30 ms")
	}
	return nil
}

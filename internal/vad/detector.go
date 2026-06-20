package vad

import "github.com/Seraf-seraf/voice_assistent/internal/audio"

type Activity uint8

const (
	Silence Activity = iota
	Speech
)

type Detector interface {
	Classify(frame audio.Frame) (Activity, error)
	Close() error
}

package input

import (
	"errors"

	"github.com/Seraf-seraf/voice_assistent/internal/audio"
)

var (
	ErrAudioUnavailable = errors.New("аудиовход недоступен: сборка выполнена без CGO")
	ErrSourceClosed     = errors.New("источник аудио закрыт")
	ErrSourceRunning    = errors.New("источник аудио уже запущен")
)

type MalgoOptions struct {
	Format         audio.Format
	QueueSize      int
	CaptureDevice  string
	PeriodSizeInMS uint32
}

type CaptureDevice struct {
	ID        string
	Name      string
	IsDefault bool
}

package input

import (
	"context"

	"github.com/Seraf-seraf/voice_assistent/internal/audio"
)

// Source запускает аудиоустройство и публикует канонические audio frames.
// Run блокируется до отмены context или ошибки устройства.
type Source interface {
	Run(ctx context.Context) error
	Frames() <-chan audio.Frame
	DroppedFrames() uint64
	Close() error
}

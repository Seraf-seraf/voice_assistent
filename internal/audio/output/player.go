package output

import (
	"context"
	"errors"

	"github.com/Seraf-seraf/voice_assistent/internal/audio"
)

type Player interface {
	Play(context.Context, audio.PCM) error
}

var ErrUnsupportedPlatform = errors.New("аудиовыход не поддерживается на этой платформе")

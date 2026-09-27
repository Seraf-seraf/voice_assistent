package tts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Seraf-seraf/voice_assistent/internal/audio"
)

const MaxTextRunes = 200

var ErrInvalidText = errors.New("некорректный текст для синтеза")

func ValidateText(text string) error {
	if !utf8.ValidString(text) || strings.ContainsRune(text, '\x00') || strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > MaxTextRunes {
		return fmt.Errorf("%w: текст должен содержать от 1 до %d непустых UTF-8 рун без NUL", ErrInvalidText, MaxTextRunes)
	}
	return nil
}

type Synthesizer interface {
	Synthesize(context.Context, string) (audio.PCM, error)
}

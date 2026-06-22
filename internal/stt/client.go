package stt

import (
	"context"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/audio"
)

type Transcript struct {
	Text     string
	Duration time.Duration
}

type Client interface {
	Transcribe(ctx context.Context, utterance audio.Utterance) (Transcript, error)
}

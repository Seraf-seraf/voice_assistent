//go:build linux && amd64 && cgo && !android && !musl

package bootstrap

import (
	"context"
	"fmt"

	"github.com/Seraf-seraf/voice_assistent/internal/platform/audio/output/pulse"
)

func validateSpeechPlayerOptions(options speechPlayerOptions) error {
	return (pulse.Options{Device: options.Device, SampleRate: options.SampleRate}).Validate()
}

func openPlatformSpeechPlayer(ctx context.Context, options speechPlayerOptions) (closeableSpeechPlayer, error) {
	player, err := pulse.Open(ctx, pulse.Options{Device: options.Device, SampleRate: options.SampleRate})
	if err != nil {
		return nil, fmt.Errorf("открыть PulseAudio: %w", err)
	}
	return player, nil
}

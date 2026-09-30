//go:build windows && amd64 && cgo

package bootstrap

import (
	"context"

	"github.com/Seraf-seraf/voice_assistent/internal/platform/audio/output/wasapi"
)

func validateSpeechPlayerOptions(options speechPlayerOptions) error {
	return (wasapi.Options{Device: options.Device, SampleRate: options.SampleRate}).Validate()
}

func openPlatformSpeechPlayer(ctx context.Context, options speechPlayerOptions) (closeableSpeechPlayer, error) {
	return wasapi.Open(ctx, wasapi.Options{Device: options.Device, SampleRate: options.SampleRate})
}

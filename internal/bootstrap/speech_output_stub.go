//go:build (!linux || !amd64 || !cgo || android || musl) && (!windows || !amd64 || !cgo)

package bootstrap

import (
	"context"

	"github.com/Seraf-seraf/voice_assistent/internal/service/audio/output"
)

func validateSpeechPlayerOptions(speechPlayerOptions) error {
	return output.ErrUnsupportedPlatform
}

func openPlatformSpeechPlayer(context.Context, speechPlayerOptions) (closeableSpeechPlayer, error) {
	return nil, output.ErrUnsupportedPlatform
}

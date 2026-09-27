//go:build !linux || !amd64 || !cgo || android || musl

package alsa

import (
	"context"
	"fmt"
)

func Open(ctx context.Context, options Options) (*Player, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("%w", ErrUnsupportedPlatform)
}

//go:build !windows || !amd64 || !cgo

package wasapi

import "context"

func Open(context.Context, Options) (*Player, error) {
	return nil, ErrUnsupportedPlatform
}

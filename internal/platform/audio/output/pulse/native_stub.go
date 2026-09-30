//go:build !linux || !amd64 || !cgo || android || musl

package pulse

import "context"

func Open(context.Context, Options) (*Player, error) {
	return nil, ErrUnsupportedPlatform
}

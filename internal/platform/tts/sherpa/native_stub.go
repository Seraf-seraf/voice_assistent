//go:build (!linux || !amd64 || !cgo || android || musl) && (!windows || !amd64 || !cgo)

package sherpa

import "context"

func Open(context.Context, Options) (*Engine, error) {
	return nil, ErrUnsupportedPlatform
}

func openNativeBackend(ctx context.Context, _ Options) (backend, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, ErrUnsupportedPlatform
}

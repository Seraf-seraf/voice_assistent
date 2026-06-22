//go:build !cgo

package input

// NewMalgoSource сообщает об отсутствии miniaudio в сборке без CGO.
func NewMalgoSource(_ MalgoOptions) (Source, error) {
	return nil, ErrAudioUnavailable
}

// ListCaptureDevices сообщает об отсутствии miniaudio в сборке без CGO.
func ListCaptureDevices() ([]CaptureDevice, error) {
	return nil, ErrAudioUnavailable
}

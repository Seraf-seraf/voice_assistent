//go:build !cgo

package input

import serviceinput "github.com/Seraf-seraf/voice_assistent/internal/service/audio/input"

// NewMalgoSource сообщает об отсутствии miniaudio в сборке без CGO.
func NewMalgoSource(_ MalgoOptions) (serviceinput.Source, error) {
	return nil, ErrAudioUnavailable
}

// ListCaptureDevices сообщает об отсутствии miniaudio в сборке без CGO.
func ListCaptureDevices() ([]CaptureDevice, error) {
	return nil, ErrAudioUnavailable
}

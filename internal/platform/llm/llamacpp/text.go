package llamacpp

import (
	"bytes"
	"fmt"
	"unicode/utf8"

	"github.com/Seraf-seraf/voice_assistent/internal/service/llm"
)

var forbiddenMarkers = [][]byte{[]byte("<think>"), []byte("</think>"), []byte("<|")}

type textDecoder struct{ pending []byte }

func (d *textDecoder) Push(piece []byte, emit llm.Emit) error {
	d.pending = append(d.pending, piece...)
	validLength, incomplete := validUTF8Prefix(d.pending)
	if !incomplete && validLength != len(d.pending) {
		return ErrInvalidOutput
	}
	data := d.pending[:validLength]
	safeLength, invalidMarker := markerSafePrefix(data)
	if invalidMarker {
		return ErrInvalidOutput
	}
	if safeLength > 0 {
		if err := emit(llm.TextDelta{Text: string(data[:safeLength])}); err != nil {
			return err
		}
	}
	d.pending = append(d.pending[:0], d.pending[safeLength:]...)
	return nil
}

func (d *textDecoder) Finish(emit llm.Emit) error {
	if !utf8.Valid(d.pending) {
		return ErrInvalidOutput
	}
	if len(d.pending) == 0 {
		return nil
	}
	for _, marker := range forbiddenMarkers {
		if bytes.Contains(d.pending, marker) {
			return fmt.Errorf("%w: служебный маркер", ErrInvalidOutput)
		}
	}
	if err := emit(llm.TextDelta{Text: string(d.pending)}); err != nil {
		return err
	}
	d.pending = nil
	return nil
}

func validUTF8Prefix(data []byte) (int, bool) {
	for i := 0; i < len(data); {
		if !utf8.FullRune(data[i:]) {
			return i, true
		}
		_, size := utf8.DecodeRune(data[i:])
		if size == 1 && data[i] >= utf8.RuneSelf {
			return i, false
		}
		i += size
	}
	return len(data), false
}

func markerSafePrefix(data []byte) (int, bool) {
	for i := 0; i < len(data); i++ {
		remaining := data[i:]
		for _, marker := range forbiddenMarkers {
			if bytes.HasPrefix(remaining, marker) {
				return i, true
			}
			if len(remaining) < len(marker) && bytes.Equal(remaining, marker[:len(remaining)]) {
				return i, false
			}
		}
	}
	return len(data), false
}

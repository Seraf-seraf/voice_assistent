package wasapi

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestSessionRendersSamplesInOrderAndSilencesRemainder(t *testing.T) {
	session := newSession([]float32{0.25, -0.5})
	buffer := make([]byte, 16)

	session.render(buffer, 4)
	want := []float32{0.25, -0.5, 0, 0}
	for i, sample := range want {
		got := math.Float32frombits(binary.LittleEndian.Uint32(buffer[i*4 : i*4+4]))
		if got != sample {
			t.Fatalf("sample %d = %v, want %v", i, got, sample)
		}
	}
	select {
	case <-session.finished:
	default:
		t.Fatal("session did not report completion")
	}
}

func TestSessionCancellationProducesSilenceWithoutAdvancing(t *testing.T) {
	session := newSession([]float32{0.25, -0.5})
	session.cancel()
	buffer := bytesOfSilence(8)

	session.render(buffer, 2)
	for i := 0; i < len(buffer); i++ {
		if buffer[i] != 0 {
			t.Fatalf("buffer byte %d = %d, want silence", i, buffer[i])
		}
	}
	if session.offset.Load() != 0 {
		t.Fatalf("offset = %d, want 0", session.offset.Load())
	}
}

func bytesOfSilence(size int) []byte { return make([]byte, size) }

package wasapi

import (
	"encoding/binary"
	"math"
	"sync"
	"sync/atomic"
)

type session struct {
	samples    []float32
	offset     atomic.Uint64
	cancelled  atomic.Bool
	finished   chan struct{}
	finishOnce sync.Once
}

func newSession(samples []float32) *session {
	return &session{samples: samples, finished: make(chan struct{})}
}

func (s *session) cancel() {
	s.cancelled.Store(true)
}

func (s *session) render(destination []byte, frames uint32) {
	clear(destination)
	if s.cancelled.Load() {
		return
	}
	count := int(frames)
	if count > len(destination)/4 {
		count = len(destination) / 4
	}
	offset := int(s.offset.Load())
	remaining := len(s.samples) - offset
	if count > remaining {
		count = remaining
	}
	for i := 0; i < count; i++ {
		bits := math.Float32bits(s.samples[offset+i])
		binary.LittleEndian.PutUint32(destination[i*4:i*4+4], bits)
	}
	offset += count
	s.offset.Store(uint64(offset))
	if offset == len(s.samples) {
		s.finishOnce.Do(func() { close(s.finished) })
	}
}

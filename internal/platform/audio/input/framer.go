package input

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/service/audio"
)

var ErrClosed = errors.New("формирователь PCM закрыт")

type Framer struct {
	format audio.Format
	frames chan audio.Frame

	mu           sync.Mutex
	pending      []int16
	pendingStart time.Time
	closed       bool
	dropped      atomic.Uint64
}

func NewFramer(format audio.Format, queueSize int) (*Framer, error) {
	if err := format.Validate(); err != nil {
		return nil, fmt.Errorf("формат аудио: %w", err)
	}
	if format.Channels != 1 {
		return nil, errors.New("формирователь PCM ожидает одноканальный звук")
	}
	if queueSize <= 0 {
		return nil, errors.New("размер очереди кадров должен быть положительным")
	}

	samplesPerFrame := format.SamplesPerFrame()
	return &Framer{
		format:  format,
		frames:  make(chan audio.Frame, queueSize),
		pending: make([]int16, 0, samplesPerFrame*2),
	}, nil
}

// WritePCM принимает mono PCM16 little-endian. firstSampleAt обозначает время
// захвата первого sample переданного блока. Метод не блокируется на full queue.
func (f *Framer) WritePCM(data []byte, firstSampleAt time.Time) error {
	if len(data)%2 != 0 {
		return errors.New("блок PCM16 должен содержать чётное число байт")
	}
	if len(data) > 0 && firstSampleAt.IsZero() {
		return errors.New("временная метка первого отсчёта обязательна")
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return ErrClosed
	}
	if len(data) == 0 {
		return nil
	}
	if len(f.pending) == 0 {
		f.pendingStart = firstSampleAt
	}

	for offset := 0; offset < len(data); offset += 2 {
		f.pending = append(f.pending, int16(binary.LittleEndian.Uint16(data[offset:offset+2])))
	}
	f.publishFrames()
	return nil
}

func (f *Framer) Frames() <-chan audio.Frame {
	return f.frames
}

func (f *Framer) DroppedFrames() uint64 {
	return f.dropped.Load()
}

func (f *Framer) Reset() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return ErrClosed
	}
	f.pending = f.pending[:0]
	f.pendingStart = time.Time{}
	return nil
}

func (f *Framer) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil
	}
	f.closed = true
	f.pending = nil
	f.pendingStart = time.Time{}
	close(f.frames)
	return nil
}

func (f *Framer) publishFrames() {
	samplesPerFrame := f.format.SamplesPerFrame()
	for len(f.pending) >= samplesPerFrame {
		samples := make([]int16, samplesPerFrame)
		copy(samples, f.pending[:samplesPerFrame])
		frame := audio.Frame{Samples: samples, CapturedAt: f.pendingStart}

		select {
		case f.frames <- frame:
		default:
			f.dropped.Add(1)
		}

		copy(f.pending, f.pending[samplesPerFrame:])
		f.pending = f.pending[:len(f.pending)-samplesPerFrame]
		f.pendingStart = f.pendingStart.Add(f.format.FrameDuration)
	}
	if len(f.pending) == 0 {
		f.pendingStart = time.Time{}
	}
}

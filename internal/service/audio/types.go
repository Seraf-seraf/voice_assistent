package audio

import (
	"errors"
	"fmt"
	"time"
)

type Format struct {
	SampleRate    int
	Channels      int
	FrameDuration time.Duration
}

func (f Format) Validate() error {
	if f.SampleRate <= 0 {
		return errors.New("частота дискретизации должна быть положительной")
	}
	if f.Channels <= 0 {
		return errors.New("число каналов должно быть положительным")
	}
	if f.FrameDuration <= 0 {
		return errors.New("длительность кадра должна быть положительной")
	}
	if int64(f.SampleRate)*f.FrameDuration.Nanoseconds()%int64(time.Second) != 0 {
		return errors.New("длительность кадра должна содержать целое число отсчётов")
	}
	return nil
}

func (f Format) SamplesPerFrame() int {
	return int(int64(f.SampleRate) * f.FrameDuration.Nanoseconds() / int64(time.Second) * int64(f.Channels))
}

type Frame struct {
	Samples    []int16
	CapturedAt time.Time
}

func (f Frame) Validate(format Format) error {
	if f.CapturedAt.IsZero() {
		return errors.New("временная метка кадра обязательна")
	}
	if len(f.Samples) != format.SamplesPerFrame() {
		return fmt.Errorf("кадр содержит %d отсчётов, требуется %d", len(f.Samples), format.SamplesPerFrame())
	}
	return nil
}

type Utterance struct {
	ID         uint64
	Samples    []int16
	Format     Format
	StartedAt  time.Time
	EndedAt    time.Time
	CapturedAt time.Time
}

func (u Utterance) Duration() time.Duration {
	if u.Format.SampleRate <= 0 || u.Format.Channels <= 0 {
		return 0
	}
	samplesPerChannel := len(u.Samples) / u.Format.Channels
	return time.Duration(int64(samplesPerChannel) * int64(time.Second) / int64(u.Format.SampleRate))
}

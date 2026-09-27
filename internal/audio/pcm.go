package audio

import (
	"fmt"
	"math"
)

const (
	MinPCMSampleRate = 1
	MaxPCMSampleRate = 192000
	minPCMSample     = -1.0
	maxPCMSample     = 1.0
)

// PCM содержит одноканальные нормализованные float32 samples.
// SampleRate — samples в секунду, len(Samples) — число mono frames.
type PCM struct {
	Samples    []float32
	SampleRate int
}

func (p PCM) Validate() error {
	if p.SampleRate < MinPCMSampleRate || p.SampleRate > MaxPCMSampleRate {
		return fmt.Errorf("PCM.SampleRate должен быть в диапазоне [%d, %d]", MinPCMSampleRate, MaxPCMSampleRate)
	}
	if len(p.Samples) == 0 {
		return fmt.Errorf("PCM.Samples не должен быть пустым")
	}
	for i, sample := range p.Samples {
		if math.IsNaN(float64(sample)) || math.IsInf(float64(sample), 0) || sample < minPCMSample || sample > maxPCMSample {
			return fmt.Errorf("PCM.Samples[%d] должен быть конечным значением в [%g, %g]", i, minPCMSample, maxPCMSample)
		}
	}
	return nil
}

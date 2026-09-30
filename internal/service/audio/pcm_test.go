package audio

import (
	"math"
	"strings"
	"testing"
)

func TestPCMValidate(t *testing.T) {
	tests := []struct {
		name    string
		pcm     PCM
		wantErr string
	}{
		{name: "zero rate", pcm: PCM{Samples: []float32{0}, SampleRate: 0}, wantErr: "SampleRate"},
		{name: "valid rate", pcm: PCM{Samples: []float32{-1, 1}, SampleRate: 22050}},
		{name: "upper rate", pcm: PCM{Samples: []float32{0}, SampleRate: 192000}},
		{name: "rate above maximum", pcm: PCM{Samples: []float32{0}, SampleRate: 192001}, wantErr: "SampleRate"},
		{name: "empty", pcm: PCM{SampleRate: 22050}, wantErr: "Samples"},
		{name: "nan", pcm: PCM{Samples: []float32{float32(math.NaN())}, SampleRate: 22050}, wantErr: "Samples[0]"},
		{name: "positive infinity", pcm: PCM{Samples: []float32{float32(math.Inf(1))}, SampleRate: 22050}, wantErr: "Samples[0]"},
		{name: "negative infinity", pcm: PCM{Samples: []float32{float32(math.Inf(-1))}, SampleRate: 22050}, wantErr: "Samples[0]"},
		{name: "below range", pcm: PCM{Samples: []float32{-1.01}, SampleRate: 22050}, wantErr: "Samples[0]"},
		{name: "above range", pcm: PCM{Samples: []float32{1.01}, SampleRate: 22050}, wantErr: "Samples[0]"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.pcm.Validate()
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Validate() error = %v, want field %q", err, test.wantErr)
			}
		})
	}
}

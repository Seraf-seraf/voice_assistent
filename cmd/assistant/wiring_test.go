package main

import (
	"testing"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/config"
)

func TestNewVADComponents(t *testing.T) {
	audioCfg := config.Default().Audio
	vadCfg := config.Default().VAD
	if _, err := newVADComponents(audioCfg, vadCfg); err != nil {
		t.Fatalf("newVADComponents() error: %v", err)
	}
}

func TestNewVADComponentsRejectsWebRTCDetectorSettings(t *testing.T) {
	tests := []struct {
		name   string
		modify func(*config.AudioConfig, *config.VADConfig)
	}{
		{
			name: "sample rate",
			modify: func(audioCfg *config.AudioConfig, _ *config.VADConfig) {
				audioCfg.SampleRate = 8000
			},
		},
		{
			name: "channels",
			modify: func(audioCfg *config.AudioConfig, _ *config.VADConfig) {
				audioCfg.Channels = 2
			},
		},
		{
			name: "frame duration",
			modify: func(audioCfg *config.AudioConfig, _ *config.VADConfig) {
				audioCfg.FrameMS = 15
			},
		},
		{
			name: "aggressiveness",
			modify: func(_ *config.AudioConfig, vadCfg *config.VADConfig) {
				vadCfg.Aggressiveness = 4
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			audioCfg := config.Default().Audio
			vadCfg := config.Default().VAD
			test.modify(&audioCfg, &vadCfg)
			if _, err := newVADComponents(audioCfg, vadCfg); err == nil {
				t.Fatal("newVADComponents() succeeded, want detector error")
			}
		})
	}
}

func TestNewVADComponentsRejectsSegmenterSettings(t *testing.T) {
	tests := []struct {
		name   string
		modify func(*config.VADConfig)
	}{
		{
			name: "invalid intervals",
			modify: func(vadCfg *config.VADConfig) {
				vadCfg.PreRoll = config.Duration(-time.Millisecond)
			},
		},
		{
			name: "max utterance not greater than min speech",
			modify: func(vadCfg *config.VADConfig) {
				vadCfg.MaxUtterance = vadCfg.MinSpeech
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			audioCfg := config.Default().Audio
			vadCfg := config.Default().VAD
			test.modify(&vadCfg)
			if _, err := newVADComponents(audioCfg, vadCfg); err == nil {
				t.Fatal("newVADComponents() succeeded, want segmenter error")
			}
		})
	}
}

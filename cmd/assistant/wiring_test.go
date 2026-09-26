package main

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/audio"
	"github.com/Seraf-seraf/voice_assistent/internal/config"
)

func TestNewAudioFormat(t *testing.T) {
	format := newAudioFormat(config.AudioConfig{SampleRate: 16000, Channels: 1, FrameMS: 20})
	want := audio.Format{SampleRate: 16000, Channels: 1, FrameDuration: 20 * time.Millisecond}
	if format != want {
		t.Fatalf("newAudioFormat() = %+v, want %+v", format, want)
	}
}

func TestNewTranscriberAndAssistantRuntimeRequireDependencies(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	client, err := newSTTClient(config.Default().STT)
	if err != nil {
		t.Fatal(err)
	}
	transcriber, err := newTranscriber(client, log)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newAssistantRuntime(nil, transcriber); err == nil {
		t.Fatal("newAssistantRuntime(nil, transcriber) succeeded")
	}
}

func TestNewVADComponents(t *testing.T) {
	format := newAudioFormat(config.Default().Audio)
	vadCfg := config.Default().VAD
	if _, err := newVADComponents(format, vadCfg); err != nil {
		t.Fatalf("newVADComponents() error: %v", err)
	}
}

func TestNewSTTClient(t *testing.T) {
	if _, err := newSTTClient(config.Default().STT); err != nil {
		t.Fatalf("newSTTClient() error: %v", err)
	}
}

func TestNewSTTClientRejectsInvalidOptions(t *testing.T) {
	tests := []struct {
		name   string
		modify func(*config.STTConfig)
	}{
		{
			name: "URL without http or https",
			modify: func(cfg *config.STTConfig) {
				cfg.URL = "ftp://example.com/transcribe"
			},
		},
		{
			name: "URL without host",
			modify: func(cfg *config.STTConfig) {
				cfg.URL = "http:///transcribe"
			},
		},
		{
			name: "URL with credentials",
			modify: func(cfg *config.STTConfig) {
				cfg.URL = "http://user:secret@example.com/transcribe"
			},
		},
		{
			name: "non-positive timeout",
			modify: func(cfg *config.STTConfig) {
				cfg.Timeout = 0
			},
		},
		{
			name: "non-positive response limit",
			modify: func(cfg *config.STTConfig) {
				cfg.MaxResponseBytes = 0
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Default().STT
			test.modify(&cfg)
			if _, err := newSTTClient(cfg); err == nil {
				t.Fatal("newSTTClient() succeeded, want HTTP client error")
			}
		})
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
			format := newAudioFormat(audioCfg)
			if _, err := newVADComponents(format, vadCfg); err == nil {
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
			format := newAudioFormat(config.Default().Audio)
			vadCfg := config.Default().VAD
			test.modify(&vadCfg)
			if _, err := newVADComponents(format, vadCfg); err == nil {
				t.Fatal("newVADComponents() succeeded, want segmenter error")
			}
		})
	}
}

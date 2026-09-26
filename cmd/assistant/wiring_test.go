package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/audio"
	"github.com/Seraf-seraf/voice_assistent/internal/config"
	"github.com/Seraf-seraf/voice_assistent/internal/stt"
	"github.com/Seraf-seraf/voice_assistent/internal/vad"
)

func TestNewAudioFormat(t *testing.T) {
	format := newAudioFormat(config.AudioConfig{SampleRate: 16000, Channels: 1, FrameMS: 20})
	want := audio.Format{SampleRate: 16000, Channels: 1, FrameDuration: 20 * time.Millisecond}
	if format != want {
		t.Fatalf("newAudioFormat() = %+v, want %+v", format, want)
	}
}

func TestCompositionFactoriesOwnTheirValidation(t *testing.T) {
	transcriptCfg := config.Default().Transcript
	if _, err := newTranscriptNormalizer(transcriptCfg); err != nil {
		t.Fatal(err)
	}
	transcriptCfg.MinSignificantRunes = 0
	if _, err := newTranscriptNormalizer(transcriptCfg); err == nil {
		t.Fatal("normalizer accepted invalid minimum")
	}
	transcriptCfg = config.Default().Transcript
	transcriptCfg.IgnoredExact = []string{"  "}
	if _, err := newTranscriptNormalizer(transcriptCfg); err == nil {
		t.Fatal("normalizer accepted empty ignored exact")
	}
	transcriptCfg = config.Default().Transcript
	transcriptCfg.IgnoredPatterns = []string{"["}
	if _, err := newTranscriptNormalizer(transcriptCfg); err == nil {
		t.Fatal("normalizer accepted invalid pattern")
	}

	appCfg, wakeCfg := config.Default().App, config.Default().Wake
	if _, err := newControlRouter(config.AppConfig{Mode: "mystery"}, wakeCfg); err == nil {
		t.Fatal("router accepted unknown mode")
	}
	wakeApp := appCfg
	wakeApp.Mode = config.ModeWake
	if _, err := newControlRouter(wakeApp, config.WakeConfig{ActivationWindow: config.Duration(time.Second)}); err == nil {
		t.Fatal("wake router accepted no phrases")
	}
	if _, err := newControlRouter(wakeApp, config.WakeConfig{Phrases: []string{"ассистент"}}); err == nil {
		t.Fatal("wake router accepted zero window")
	}
	for _, mode := range []string{config.ModeAlways, config.ModePTT} {
		if _, err := newControlRouter(config.AppConfig{Mode: mode}, config.WakeConfig{}); err != nil {
			t.Errorf("%s router rejected empty wake config: %v", mode, err)
		}
	}

	if _, err := newDialogueManager(config.Default().App, config.Default().Dialogue); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		app      config.AppConfig
		dialogue config.DialogueConfig
	}{
		{name: "empty system prompt", app: config.AppConfig{ResponsePolicy: "policy"}, dialogue: config.Default().Dialogue},
		{name: "empty response policy", app: config.AppConfig{SystemPrompt: "prompt"}, dialogue: config.Default().Dialogue},
		{name: "short history", app: config.Default().App, dialogue: config.DialogueConfig{MaxHistoryMessages: 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := newDialogueManager(test.app, test.dialogue); err == nil {
				t.Fatal("dialogue manager accepted invalid options")
			}
		})
	}
}

type wiringSTTClient struct {
	result stt.Transcript
}

func (c wiringSTTClient) Transcribe(context.Context, audio.Utterance) (stt.Transcript, error) {
	return c.result, nil
}

func TestTranscriberForwardsQueryAndLogsOnlyMetadata(t *testing.T) {
	var output bytes.Buffer
	log := slog.New(slog.NewTextHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cfg := config.Default()
	normalizer, err := newTranscriptNormalizer(cfg.Transcript)
	if err != nil {
		t.Fatal(err)
	}
	controlRouter, err := newControlRouter(cfg.App, cfg.Wake)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := newDialogueManager(cfg.App, cfg.Dialogue)
	if err != nil {
		t.Fatal(err)
	}
	processor, err := newInputProcessor(normalizer, controlRouter, manager, log)
	if err != nil {
		t.Fatal(err)
	}
	client := wiringSTTClient{result: stt.Transcript{Text: "секретная пользовательская фраза", Duration: 125 * time.Millisecond}}
	transcriber, err := newTranscriber(client, log, processor)
	if err != nil {
		t.Fatal(err)
	}
	if err := transcriber.Handle(context.Background(), vad.SpeechEnded{Utterance: audio.Utterance{ID: 77}}); err != nil {
		t.Fatal(err)
	}
	transcriber.CloseInput()
	if err := transcriber.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	logged := output.String()
	if strings.Contains(logged, "секретная пользовательская фраза") {
		t.Fatalf("user text leaked into log: %s", logged)
	}
	if !strings.Contains(logged, "utterance_id=77") || !strings.Contains(logged, "stt_duration=125ms") {
		t.Fatalf("safe metadata missing from log: %s", logged)
	}
	if len(manager.Snapshot().Messages) != 0 {
		t.Fatal("input processing started a dialogue turn")
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

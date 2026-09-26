package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/assistant"
	"github.com/Seraf-seraf/voice_assistent/internal/audio"
	"github.com/Seraf-seraf/voice_assistent/internal/audio/input"
	"github.com/Seraf-seraf/voice_assistent/internal/audio/listener"
	"github.com/Seraf-seraf/voice_assistent/internal/config"
	"github.com/Seraf-seraf/voice_assistent/internal/control/router"
	"github.com/Seraf-seraf/voice_assistent/internal/dialogue"
	"github.com/Seraf-seraf/voice_assistent/internal/stt"
	"github.com/Seraf-seraf/voice_assistent/internal/transcript"
	"github.com/Seraf-seraf/voice_assistent/internal/vad"
)

type vadComponents struct {
	detector  vad.Detector
	segmenter *vad.Segmenter
}

const (
	speechEventQueueSize   = 2
	transcriptionQueueSize = 1
)

type audioInputComponents struct {
	source   input.Source
	listener *listener.Listener
}

func newAudioFormat(cfg config.AudioConfig) audio.Format {
	return audio.Format{
		SampleRate:    cfg.SampleRate,
		Channels:      cfg.Channels,
		FrameDuration: time.Duration(cfg.FrameMS) * time.Millisecond,
	}
}

func newVADComponents(format audio.Format, vadCfg config.VADConfig) (vadComponents, error) {
	detector, err := vad.NewWebRTCDetector(format, vadCfg.Aggressiveness)
	if err != nil {
		return vadComponents{}, fmt.Errorf("создать WebRTC detector: %w", err)
	}

	segmenter, err := vad.NewSegmenter(vad.Settings{
		Format:       format,
		PreRoll:      time.Duration(vadCfg.PreRoll),
		MinSpeech:    time.Duration(vadCfg.MinSpeech),
		EndSilence:   time.Duration(vadCfg.EndSilence),
		MaxUtterance: time.Duration(vadCfg.MaxUtterance),
	})
	if err != nil {
		if closeErr := detector.Close(); closeErr != nil {
			return vadComponents{}, errors.Join(
				fmt.Errorf("создать segmenter: %w", err),
				fmt.Errorf("закрыть WebRTC detector: %w", closeErr),
			)
		}
		return vadComponents{}, fmt.Errorf("создать segmenter: %w", err)
	}

	return vadComponents{detector: detector, segmenter: segmenter}, nil
}

func newAudioInputComponents(
	format audio.Format,
	cfg config.AudioConfig,
	vadComponents vadComponents,
) (audioInputComponents, error) {
	source, err := input.NewMalgoSource(input.MalgoOptions{
		Format:        format,
		QueueSize:     cfg.BufferFrames,
		CaptureDevice: cfg.InputDevice,
	})
	if err != nil {
		return audioInputComponents{}, fmt.Errorf("создать audio source: %w", err)
	}

	audioListener, err := listener.New(source, vadComponents.detector, vadComponents.segmenter, speechEventQueueSize)
	if err != nil {
		if closeErr := source.Close(); closeErr != nil {
			return audioInputComponents{}, errors.Join(
				fmt.Errorf("создать listener: %w", err),
				fmt.Errorf("закрыть audio source: %w", closeErr),
			)
		}
		return audioInputComponents{}, fmt.Errorf("создать listener: %w", err)
	}

	return audioInputComponents{source: source, listener: audioListener}, nil
}

func newSTTClient(cfg config.STTConfig) (stt.Client, error) {
	client, err := stt.NewHTTPClient(stt.HTTPOptions{
		Endpoint:         cfg.URL,
		Timeout:          cfg.Timeout.Std(),
		MaxResponseBytes: cfg.MaxResponseBytes,
		APIKey:           cfg.APIKey,
	})
	if err != nil {
		return nil, fmt.Errorf("создать STT HTTP client: %w", err)
	}
	return client, nil
}

func newTranscriptNormalizer(cfg config.TranscriptConfig) (*transcript.Normalizer, error) {
	return transcript.NewNormalizer(transcript.Options{
		MinSignificantRunes: cfg.MinSignificantRunes,
		IgnoredExact:        cfg.IgnoredExact,
		IgnoredPatterns:     cfg.IgnoredPatterns,
	})
}

func newControlRouter(appCfg config.AppConfig, wakeCfg config.WakeConfig) (*router.Router, error) {
	mode, err := router.ParseMode(appCfg.Mode)
	if err != nil {
		return nil, err
	}
	return router.New(router.Options{
		Mode:        mode,
		WakePhrases: wakeCfg.Phrases,
		WakeWindow:  wakeCfg.ActivationWindow.Std(),
	})
}

func newDialogueManager(appCfg config.AppConfig, dialogueCfg config.DialogueConfig) (*dialogue.Manager, error) {
	return dialogue.New(dialogue.Options{
		SystemPrompt:       appCfg.SystemPrompt,
		ResponsePolicy:     appCfg.ResponsePolicy,
		MaxHistoryMessages: dialogueCfg.MaxHistoryMessages,
	})
}

func newInputProcessor(
	normalizer *transcript.Normalizer,
	controlRouter *router.Router,
	manager *dialogue.Manager,
	log *slog.Logger,
) (*assistant.InputProcessor, error) {
	return assistant.NewInputProcessor(normalizer, controlRouter, manager, func(_ context.Context, query assistant.Query) error {
		log.Debug("Получен запрос", "utterance_id", query.UtteranceID)
		return nil
	}, time.Now)
}

func newTranscriber(client stt.Client, log *slog.Logger, processor *assistant.InputProcessor) (*assistant.Transcriber, error) {
	return assistant.NewTranscriber(client, func(ctx context.Context, result assistant.Transcription) error {
		log.Debug("Речь распознана", "utterance_id", result.UtteranceID, "stt_duration", result.Duration)
		return processor.Handle(ctx, result)
	}, transcriptionQueueSize)
}

func newAssistantRuntime(audioListener *listener.Listener, transcriber *assistant.Transcriber) (*assistant.Runtime, error) {
	return assistant.NewRuntime(audioListener, transcriber)
}

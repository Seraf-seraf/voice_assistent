package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/app/assistant"
	"github.com/Seraf-seraf/voice_assistent/internal/app/listener"
	"github.com/Seraf-seraf/voice_assistent/internal/bootstrap/config"
	"github.com/Seraf-seraf/voice_assistent/internal/platform/audio/input"
	"github.com/Seraf-seraf/voice_assistent/internal/platform/llm/llamacpp"
	stthttp "github.com/Seraf-seraf/voice_assistent/internal/platform/stt/http"
	webrtcvad "github.com/Seraf-seraf/voice_assistent/internal/platform/vad/webrtc"
	"github.com/Seraf-seraf/voice_assistent/internal/service/audio"
	audioinput "github.com/Seraf-seraf/voice_assistent/internal/service/audio/input"
	"github.com/Seraf-seraf/voice_assistent/internal/service/control/router"
	"github.com/Seraf-seraf/voice_assistent/internal/service/dialogue"
	"github.com/Seraf-seraf/voice_assistent/internal/service/llm"
	"github.com/Seraf-seraf/voice_assistent/internal/service/stt"
	"github.com/Seraf-seraf/voice_assistent/internal/service/transcript"
	"github.com/Seraf-seraf/voice_assistent/internal/service/vad"
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
	source   audioinput.Source
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
	detector, err := webrtcvad.NewWebRTCDetector(format, vadCfg.Aggressiveness)
	if err != nil {
		return vadComponents{}, fmt.Errorf("создать детектор WebRTC: %w", err)
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
				fmt.Errorf("создать сегментатор: %w", err),
				fmt.Errorf("закрыть детектор WebRTC: %w", closeErr),
			)
		}
		return vadComponents{}, fmt.Errorf("создать сегментатор: %w", err)
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
		return audioInputComponents{}, fmt.Errorf("создать источник аудио: %w", err)
	}

	audioListener, err := listener.New(source, vadComponents.detector, vadComponents.segmenter, speechEventQueueSize)
	if err != nil {
		if closeErr := source.Close(); closeErr != nil {
			return audioInputComponents{}, errors.Join(
				fmt.Errorf("создать слушатель: %w", err),
				fmt.Errorf("закрыть источник аудио: %w", closeErr),
			)
		}
		return audioInputComponents{}, fmt.Errorf("создать слушатель: %w", err)
	}

	return audioInputComponents{source: source, listener: audioListener}, nil
}

func newSTTClient(cfg config.STTConfig) (stt.Client, error) {
	client, err := stthttp.NewHTTPClient(stthttp.HTTPOptions{
		Endpoint:         cfg.URL,
		Timeout:          cfg.Timeout.Std(),
		MaxResponseBytes: cfg.MaxResponseBytes,
		APIKey:           cfg.APIKey,
	})
	if err != nil {
		return nil, fmt.Errorf("создать HTTP-клиент STT: %w", err)
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

func newGenerationOptions(cfg config.LLMConfig) (llm.Options, error) {
	options := llm.Options{Temperature: cfg.Temperature, MaxTokens: cfg.MaxTokens}
	if err := options.Validate(); err != nil {
		return llm.Options{}, err
	}
	return options, nil
}

func newLocalGenerator(ctx context.Context, cfg config.LLMConfig) (*llamacpp.Generator, error) {
	return llamacpp.Open(ctx, llamacpp.Options{
		ModelPath: cfg.Model, LibraryDir: cfg.LibraryDir,
		ContextSize: cfg.ContextSize, GPULayers: cfg.GPULayers,
		Threads: cfg.Threads, Timeout: cfg.Timeout.Std(),
	})
}

func loadLocalGeneratorWithStatus(
	ctx context.Context,
	cfg config.LLMConfig,
	log *slog.Logger,
	open func(context.Context, config.LLMConfig) (*llamacpp.Generator, error),
) (*llamacpp.Generator, error) {
	started := time.Now()
	modelName := filepath.Base(cfg.Model)
	log.Info("Начата загрузка модели LLM", "модель", modelName)

	generator, err := open(ctx, cfg)
	duration := time.Since(started)
	if err != nil {
		log.Error("Ошибка загрузки модели LLM", "модель", modelName, "длительность", duration)
		return nil, err
	}
	log.Info("Модель LLM загружена", "модель", modelName, "длительность", duration)
	return generator, nil
}

func newInputProcessor(
	normalizer *transcript.Normalizer,
	controlRouter *router.Router,
	manager *dialogue.Manager,
	queryHandler assistant.QueryHandler,
	log *slog.Logger,
) (*assistant.InputProcessor, error) {
	if queryHandler == nil {
		return nil, errors.New("обработчик запросов обязателен")
	}
	return assistant.NewInputProcessor(normalizer, controlRouter, manager, func(ctx context.Context, query assistant.Query) error {
		log.Debug("Получен запрос", "utterance_id", query.UtteranceID)
		return queryHandler(ctx, query)
	}, time.Now)
}

func newTranscriber(client stt.Client, log *slog.Logger, processor *assistant.InputProcessor, output io.Writer) (*assistant.Transcriber, error) {
	transcriptionOutput, err := newTranscriptionOutput(output)
	if err != nil {
		return nil, err
	}
	return assistant.NewTranscriber(client, func(ctx context.Context, result assistant.Transcription) error {
		log.Debug("Речь распознана", "utterance_id", result.UtteranceID, "stt_duration", result.Duration)
		if err := transcriptionOutput(ctx, result); err != nil {
			return err
		}
		return processor.Handle(ctx, result)
	}, transcriptionQueueSize)
}

func newAssistantRuntime(audioListener *listener.Listener, transcriber *assistant.Transcriber) (*assistant.Runtime, error) {
	return assistant.NewRuntime(audioListener, transcriber)
}

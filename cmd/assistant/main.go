package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"

	"github.com/Seraf-seraf/voice_assistent/internal/assistant"
	"github.com/Seraf-seraf/voice_assistent/internal/config"
	"github.com/Seraf-seraf/voice_assistent/internal/logger"
)

func main() {
	err := func() error {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		return run(ctx)
	}()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка запуска: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) (resultErr error) {
	configPath := flag.String("config", "", "путь к YAML-конфигурации")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("загрузить конфигурацию: %w", err)
	}

	log, err := logger.New(logger.Options{
		Level:  cfg.Log.Level,
		Format: cfg.Log.Format,
	})
	if err != nil {
		return fmt.Errorf("создать logger: %w", err)
	}

	log.Info("Конфигурация загружена", "режим", cfg.App.Mode)
	normalizer, err := newTranscriptNormalizer(cfg.Transcript)
	if err != nil {
		return fmt.Errorf("создать transcript normalizer: %w", err)
	}
	controlRouter, err := newControlRouter(cfg.App, cfg.Wake)
	if err != nil {
		return fmt.Errorf("создать control router: %w", err)
	}
	manager, err := newDialogueManager(cfg.App, cfg.Dialogue)
	if err != nil {
		return fmt.Errorf("создать dialogue manager: %w", err)
	}
	generationOptions, err := newGenerationOptions(cfg.LLM)
	if err != nil {
		return fmt.Errorf("проверить параметры генерации: %w", err)
	}
	responseHandler, err := newResponseHandler(os.Stdout)
	if err != nil {
		return fmt.Errorf("создать response handler: %w", err)
	}
	sttClient, err := newSTTClient(cfg.STT)
	if err != nil {
		return fmt.Errorf("создать STT client: %w", err)
	}
	generator, err := newLocalGenerator(ctx, cfg.LLM)
	if err != nil {
		return fmt.Errorf("загрузить локальную LLM: %w", err)
	}
	defer func() {
		if err := generator.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("закрыть локальную LLM: %w", err))
		}
	}()
	responder, err := assistant.NewResponder(manager, generator, generationOptions, responseHandler)
	if err != nil {
		return fmt.Errorf("создать responder: %w", err)
	}
	processor, err := newInputProcessor(normalizer, controlRouter, manager, responder.Handle, log)
	if err != nil {
		return fmt.Errorf("создать input processor: %w", err)
	}
	transcriber, err := newTranscriber(sttClient, log, processor, os.Stdout)
	if err != nil {
		return fmt.Errorf("создать transcriber: %w", err)
	}

	format := newAudioFormat(cfg.Audio)
	components, err := newVADComponents(format, cfg.VAD)
	if err != nil {
		return fmt.Errorf("создать VAD: %w", err)
	}
	audioInput, err := newAudioInputComponents(format, cfg.Audio, components)
	if err != nil {
		return closeStartupDetector(log, components, fmt.Errorf("создать audio input: %w", err))
	}
	runtime, err := newAssistantRuntime(audioInput.listener, transcriber)
	if err != nil {
		return closeStartupAudioInput(log, audioInput, components, fmt.Errorf("создать runtime: %w", err))
	}
	return runtime.Run(ctx)
}

func closeStartupDetector(log *slog.Logger, components vadComponents, cause error) error {
	if err := components.detector.Close(); err != nil {
		log.Error("Закрыть VAD detector", "ошибка", err)
		return errors.Join(cause, fmt.Errorf("закрыть VAD detector: %w", err))
	}
	return cause
}

func closeStartupAudioInput(log *slog.Logger, audioInput audioInputComponents, components vadComponents, cause error) error {
	if err := audioInput.source.Close(); err != nil {
		log.Error("Закрыть audio source", "ошибка", err)
		cause = errors.Join(cause, fmt.Errorf("закрыть audio source: %w", err))
	}
	return closeStartupDetector(log, components, cause)
}

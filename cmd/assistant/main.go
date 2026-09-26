package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"

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

func run(ctx context.Context) error {
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
	format := newAudioFormat(cfg.Audio)
	components, err := newVADComponents(format, cfg.VAD)
	if err != nil {
		return fmt.Errorf("создать VAD: %w", err)
	}
	if _, err := newSTTClient(cfg.STT); err != nil {
		return closeStartupDetector(log, components, fmt.Errorf("создать STT client: %w", err))
	}
	audioInput, err := newAudioInputComponents(format, cfg.Audio, components)
	if err != nil {
		return closeStartupDetector(log, components, fmt.Errorf("создать audio input: %w", err))
	}
	runtime, err := newAssistantRuntime(audioInput.listener, log)
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

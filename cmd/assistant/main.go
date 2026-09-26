package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Seraf-seraf/voice_assistent/internal/config"
	"github.com/Seraf-seraf/voice_assistent/internal/logger"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка запуска: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
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
	defer func() {
		if err := components.detector.Close(); err != nil {
			log.Error("Закрыть VAD detector", "ошибка", err)
		}
	}()
	audioInput, err := newAudioInputComponents(format, cfg.Audio, components)
	if err != nil {
		return fmt.Errorf("создать audio input: %w", err)
	}
	defer func() {
		if err := audioInput.source.Close(); err != nil {
			log.Error("Закрыть audio source", "ошибка", err)
		}
	}()
	if _, err := newSTTClient(cfg.STT); err != nil {
		return fmt.Errorf("создать STT client: %w", err)
	}
	return nil
}

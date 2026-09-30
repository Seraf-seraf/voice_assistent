package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/Seraf-seraf/voice_assistent/internal/app/assistant"
	"github.com/Seraf-seraf/voice_assistent/internal/bootstrap/config"
	"github.com/Seraf-seraf/voice_assistent/internal/platform/logger"
	"github.com/Seraf-seraf/voice_assistent/internal/service/diagnostics"
)

// Run загружает конфигурацию, создаёт компоненты и запускает приложение.
func Run(ctx context.Context, configPath string) (resultErr error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("загрузить конфигурацию: %w", err)
	}

	log, err := logger.New(logger.Options{
		Level:  cfg.Log.Level,
		Format: cfg.Log.Format,
	})
	if err != nil {
		return fmt.Errorf("создать журнал: %w", err)
	}

	log.Info("Конфигурация загружена", "режим", cfg.App.Mode)
	ctx = diagnostics.WithObserver(ctx, diagnosticLogger(log))
	normalizer, err := newTranscriptNormalizer(cfg.Transcript)
	if err != nil {
		return fmt.Errorf("создать нормализатор транскрипта: %w", err)
	}
	controlRouter, err := newControlRouter(cfg.App, cfg.Wake)
	if err != nil {
		return fmt.Errorf("создать маршрутизатор команд: %w", err)
	}
	manager, err := newDialogueManager(cfg.App, cfg.Dialogue)
	if err != nil {
		return fmt.Errorf("создать менеджер диалога: %w", err)
	}
	generationOptions, err := newGenerationOptions(cfg.LLM)
	if err != nil {
		return fmt.Errorf("проверить параметры генерации: %w", err)
	}
	responseOutput, err := newResponseOutput(os.Stdout)
	if err != nil {
		return fmt.Errorf("создать вывод ответа: %w", err)
	}
	var selectedResponseSink assistant.ResponseSink = responseOutput
	if cfg.TTS.ModelDir != "" {
		spokenOutput, cleanupSpeechOutput, err := newSpeechOutput(ctx, cfg.TTS, cfg.Audio.OutputDevice, responseOutput)
		if err != nil {
			return fmt.Errorf("создать речевой вывод: %w", err)
		}
		selectedResponseSink = spokenOutput
		defer func() {
			if err := cleanupSpeechOutput(); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("закрыть речевой вывод: %w", err))
			}
		}()
	}
	sttClient, err := newSTTClient(cfg.STT)
	if err != nil {
		return fmt.Errorf("создать клиент STT: %w", err)
	}
	generator, err := loadLocalGeneratorWithStatus(ctx, cfg.LLM, log, newLocalGenerator)
	if err != nil {
		return fmt.Errorf("загрузить локальную LLM: %w", err)
	}
	defer func() {
		if err := generator.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("закрыть локальную LLM: %w", err))
		}
	}()
	responder, err := assistant.NewResponder(manager, generator, generationOptions, selectedResponseSink)
	if err != nil {
		return fmt.Errorf("создать обработчик ответа: %w", err)
	}
	processor, err := newInputProcessor(normalizer, controlRouter, manager, responder.Handle, log)
	if err != nil {
		return fmt.Errorf("создать обработчик ввода: %w", err)
	}
	transcriber, err := newTranscriber(sttClient, log, processor, os.Stdout)
	if err != nil {
		return fmt.Errorf("создать компонент распознавания: %w", err)
	}

	format := newAudioFormat(cfg.Audio)
	components, err := newVADComponents(format, cfg.VAD)
	if err != nil {
		return fmt.Errorf("создать VAD: %w", err)
	}
	audioInput, err := newAudioInputComponents(format, cfg.Audio, components)
	if err != nil {
		return closeStartupDetector(log, components, fmt.Errorf("создать аудиовход: %w", err))
	}
	runtime, err := newAssistantRuntime(audioInput.listener, transcriber)
	if err != nil {
		return closeStartupAudioInput(log, audioInput, components, fmt.Errorf("создать среду выполнения: %w", err))
	}
	return runtime.Run(ctx)
}

func diagnosticLogger(log *slog.Logger) diagnostics.Observer {
	return func(event diagnostics.Event) {
		attrs := []any{
			"этап", diagnosticPhaseName(event.Phase),
			"utterance_id", event.UtteranceID,
			"turn_id", event.TurnID,
		}
		if event.Duration != 0 {
			attrs = append(attrs, "длительность", event.Duration)
		}
		if event.SinceSpeechEnd != 0 {
			attrs = append(attrs, "от_конца_речи", event.SinceSpeechEnd)
		}
		if event.Runes != 0 {
			attrs = append(attrs, "число_рун", event.Runes)
		}
		if event.Frames != 0 {
			attrs = append(attrs, "число_кадров", event.Frames)
		}
		if event.SampleRate != 0 {
			attrs = append(attrs, "частота_дискретизации", event.SampleRate)
		}
		if event.Phase == "stt_failed" || event.Phase == "llm_failed" || event.Phase == "tts_failed" || event.Phase == "output_play_failed" {
			log.Warn("Ошибка на этапе обработки речи", attrs...)
			return
		}
		log.Debug("Диагностический этап обработки речи", attrs...)
	}
}

func diagnosticPhaseName(phase string) string {
	switch phase {
	case "stt_started":
		return "распознавание начато"
	case "capture_started":
		return "запись с микрофона запущена"
	case "capture_first_frame":
		return "получен первый аудиоблок микрофона"
	case "stt_completed":
		return "распознавание завершено"
	case "stt_failed":
		return "ошибка распознавания"
	case "speech_end_to_stt":
		return "от конца речи до результата распознавания"
	case "speech_end_to_query":
		return "от конца речи до начала обработки запроса"
	case "query_started":
		return "обработка запроса начата"
	case "llm_started":
		return "генерация LLM начата"
	case "llm_first_delta":
		return "первая дельта LLM получена"
	case "llm_completed":
		return "генерация LLM завершена"
	case "llm_failed":
		return "ошибка генерации LLM"
	case "phrase_ready":
		return "фраза готова к синтезу"
	case "tts_started":
		return "синтез начат"
	case "tts_completed":
		return "синтез завершён"
	case "tts_failed":
		return "ошибка синтеза"
	case "output_configured":
		return "аудиовыход настроен"
	case "output_play_requested":
		return "фраза передана в аудиовыход"
	case "output_started":
		return "воспроизведение началось"
	case "output_drained":
		return "передача звука завершена"
	case "output_play_failed":
		return "ошибка воспроизведения"
	default:
		return "неизвестный этап"
	}
}

func closeStartupDetector(log *slog.Logger, components vadComponents, cause error) error {
	if err := components.detector.Close(); err != nil {
		log.Error("Закрыть детектор VAD", "ошибка", err)
		return errors.Join(cause, fmt.Errorf("закрыть детектор VAD: %w", err))
	}
	return cause
}

func closeStartupAudioInput(log *slog.Logger, audioInput audioInputComponents, components vadComponents, cause error) error {
	if err := audioInput.source.Close(); err != nil {
		log.Error("Закрыть источник аудио", "ошибка", err)
		cause = errors.Join(cause, fmt.Errorf("закрыть источник аудио: %w", err))
	}
	return closeStartupDetector(log, components, cause)
}

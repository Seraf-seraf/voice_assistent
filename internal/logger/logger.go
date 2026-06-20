package logger

import (
	"errors"
	"io"
	"log/slog"
	"os"

	"github.com/Seraf-seraf/voice_assistent/internal/config"
)

func New(cfg config.LogConfig) (*slog.Logger, error) {
	return NewWithWriter(cfg, os.Stderr)
}

func NewWithWriter(cfg config.LogConfig, output io.Writer) (*slog.Logger, error) {
	level, err := parseLevel(cfg.Level)
	if err != nil {
		return nil, err
	}
	options := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	switch cfg.Format {
	case "text":
		handler = slog.NewTextHandler(output, options)
	case "json":
		handler = slog.NewJSONHandler(output, options)
	default:
		return nil, errors.New("неизвестный формат логов")
	}
	return slog.New(handler), nil
}

func parseLevel(value string) (slog.Level, error) {
	switch value {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, errors.New("неизвестный уровень логирования")
	}
}

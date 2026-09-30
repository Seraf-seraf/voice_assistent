package logger

import (
	"errors"
	"io"
	"log/slog"
	"os"
)

type Options struct {
	Level  string
	Format string
}

func New(options Options) (*slog.Logger, error) {
	return NewWithWriter(options, os.Stderr)
}

func NewWithWriter(options Options, output io.Writer) (*slog.Logger, error) {
	level, err := parseLevel(options.Level)
	if err != nil {
		return nil, err
	}
	handlerOptions := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	switch options.Format {
	case "text":
		handler = slog.NewTextHandler(output, handlerOptions)
	case "json":
		handler = slog.NewJSONHandler(output, handlerOptions)
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

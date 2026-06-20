package logger

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Seraf-seraf/voice_assistent/internal/config"
)

func TestNewWithWriterRespectsLevel(t *testing.T) {
	var output bytes.Buffer
	log, err := NewWithWriter(config.LogConfig{Level: "info", Format: "json"}, &output)
	if err != nil {
		t.Fatalf("NewWithWriter() error: %v", err)
	}

	log.Debug("Скрытое сообщение")
	log.Info("Ассистент запущен", "component", "app")

	text := output.String()
	if strings.Contains(text, "Скрытое") || !strings.Contains(text, "Ассистент запущен") {
		t.Fatalf("unexpected log output: %s", text)
	}
}

func TestNewWithWriterRejectsInvalidOptions(t *testing.T) {
	for _, cfg := range []config.LogConfig{
		{Level: "trace", Format: "text"},
		{Level: "info", Format: "xml"},
	} {
		if _, err := NewWithWriter(cfg, &bytes.Buffer{}); err == nil {
			t.Fatalf("NewWithWriter(%+v) succeeded, want error", cfg)
		}
	}
}

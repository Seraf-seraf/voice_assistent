package logger

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewWithWriterRespectsLevel(t *testing.T) {
	var output bytes.Buffer
	log, err := NewWithWriter(Options{Level: "info", Format: "json"}, &output)
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
	for _, options := range []Options{
		{Level: "trace", Format: "text"},
		{Level: "info", Format: "xml"},
	} {
		if _, err := NewWithWriter(options, &bytes.Buffer{}); err == nil {
			t.Fatalf("NewWithWriter(%+v) succeeded, want error", options)
		}
	}
}

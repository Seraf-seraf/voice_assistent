package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadUsesDefaultsAndEnvironment(t *testing.T) {
	t.Setenv("ASSISTANT_LLM_MODEL", "qwen-test")
	t.Setenv("ASSISTANT_MODE", ModeWake)
	t.Setenv("ASSISTANT_MAX_HISTORY_MESSAGES", "12")
	t.Setenv("ASSISTANT_VAD_END_SILENCE", "900ms")
	t.Setenv("ASSISTANT_WAKE_PHRASES", "ассистент, компьютер")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.LLM.Model != "qwen-test" || cfg.App.Mode != ModeWake {
		t.Fatalf("environment overrides not applied: %+v", cfg)
	}
	if cfg.Dialogue.MaxHistoryMessages != 12 {
		t.Fatalf("MaxHistoryMessages = %d, want 12", cfg.Dialogue.MaxHistoryMessages)
	}
	if cfg.Audio.SampleRate != 16000 || cfg.VAD.EndSilence.Std() != 900*time.Millisecond {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if len(cfg.Wake.Phrases) != 2 || cfg.Wake.Phrases[1] != "компьютер" {
		t.Fatalf("Wake.Phrases = %v, want two phrases", cfg.Wake.Phrases)
	}
}

func TestLoadYAMLAndEnvironmentPrecedence(t *testing.T) {
	path := writeConfig(t, `
llm:
  model: from-file
  temperature: 0.7
vad:
  end_silence: 750ms
`)
	t.Setenv("ASSISTANT_LLM_MODEL", "from-env")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.LLM.Model != "from-env" {
		t.Fatalf("LLM.Model = %q, want from-env", cfg.LLM.Model)
	}
	if cfg.LLM.Temperature != 0.7 || cfg.VAD.EndSilence.Std() != 750*time.Millisecond {
		t.Fatalf("file values not applied: %+v", cfg)
	}
}

func TestLoadRejectsUnknownField(t *testing.T) {
	path := writeConfig(t, "llm:\n  model: test\n  typo: true\n")

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "field typo not found") {
		t.Fatalf("Load() error = %v, want unknown field error", err)
	}
}

func TestValidateRejectsCredentialsInURL(t *testing.T) {
	cfg := Default()
	cfg.LLM.Model = "test"
	cfg.STT.URL = "http://user:secret@127.0.0.1/inference"

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "credentials") {
		t.Fatalf("Validate() error = %v, want credentials error", err)
	}
}

func TestLoadRequiresModel(t *testing.T) {
	t.Setenv("ASSISTANT_LLM_MODEL", "")
	_, err := Load("")
	if err == nil || !strings.Contains(err.Error(), "llm.model") {
		t.Fatalf("Load() error = %v, want missing model error", err)
	}
}

func TestLoadRejectsInvalidEnvironment(t *testing.T) {
	t.Setenv("ASSISTANT_LLM_MODEL", "test")
	t.Setenv("ASSISTANT_TTS_SPEED", "fast")

	_, err := Load("")
	if err == nil || !strings.Contains(err.Error(), "ASSISTANT_TTS_SPEED") {
		t.Fatalf("Load() error = %v, want environment parsing error", err)
	}
}

func TestExampleConfiguration(t *testing.T) {
	t.Setenv("ASSISTANT_LLM_MODEL", "test")
	path := filepath.Join("..", "..", "config", "assistant.example.yaml")

	if _, err := Load(path); err != nil {
		t.Fatalf("Load(example) error: %v", err)
	}
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "assistant.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	return path
}

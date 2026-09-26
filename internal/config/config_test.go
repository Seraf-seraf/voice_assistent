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
	t.Setenv("ASSISTANT_TRANSCRIPT_MIN_RUNES", "3")
	t.Setenv("ASSISTANT_TRANSCRIPT_IGNORED_EXACT", "ээ, тест")

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
	if cfg.Transcript.MinSignificantRunes != 3 || len(cfg.Transcript.IgnoredExact) != 2 {
		t.Fatalf("Transcript config = %+v", cfg.Transcript)
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

func TestLoadAllowsUnconfiguredModel(t *testing.T) {
	t.Setenv("ASSISTANT_LLM_MODEL", "")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.LLM.Model != "" {
		t.Fatalf("LLM.Model = %q, want empty", cfg.LLM.Model)
	}
}

func TestLoadRejectsInvalidEnvironment(t *testing.T) {
	t.Setenv("ASSISTANT_LLM_TEMPERATURE", "hot")

	_, err := Load("")
	if err == nil || !strings.Contains(err.Error(), "ASSISTANT_LLM_TEMPERATURE") {
		t.Fatalf("Load() error = %v, want environment parsing error", err)
	}
}

func TestExampleConfiguration(t *testing.T) {
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

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadUsesDefaultsAndEnvironment(t *testing.T) {
	unsetEnv(t, "ASSISTANT_TTS_MODEL_DIR")
	unsetEnv(t, "ASSISTANT_TTS_THREADS")
	unsetEnv(t, "ASSISTANT_TTS_TIMEOUT")
	t.Setenv("ASSISTANT_LLM_MODEL", "qwen-test")
	t.Setenv("ASSISTANT_LLM_LIBRARY_DIR", "/native")
	t.Setenv("ASSISTANT_LLM_CONTEXT_SIZE", "8192")
	t.Setenv("ASSISTANT_LLM_GPU_LAYERS", "42")
	t.Setenv("ASSISTANT_LLM_THREADS", "6")
	t.Setenv("ASSISTANT_LLM_TIMEOUT", "45s")
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
	if cfg.LLM.LibraryDir != "/native" || cfg.LLM.ContextSize != 8192 || cfg.LLM.GPULayers != 42 || cfg.LLM.Threads != 6 || cfg.LLM.Timeout.Std() != 45*time.Second {
		t.Fatalf("LLM environment overrides not applied: %+v", cfg.LLM)
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
	if cfg.TTS.ModelDir != "" || cfg.TTS.Threads != 2 || cfg.TTS.Timeout.Std() != 5*time.Minute {
		t.Fatalf("TTS defaults = %+v", cfg.TTS)
	}
}

func TestLoadTTSYamlAndEnvironmentPrecedence(t *testing.T) {
	path := writeConfig(t, `
tts:
  model_dir: /models/from-file
  threads: 4
  timeout: 3m
audio:
  output_device: hw:0
`)
	t.Setenv("ASSISTANT_TTS_MODEL_DIR", "/models/from-env")
	t.Setenv("ASSISTANT_TTS_THREADS", "6")
	t.Setenv("ASSISTANT_TTS_TIMEOUT", "7m")
	t.Setenv("ASSISTANT_AUDIO_OUTPUT_DEVICE", "pulse")

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TTS.ModelDir != "/models/from-env" || cfg.TTS.Threads != 6 || cfg.TTS.Timeout.Std() != 7*time.Minute {
		t.Fatalf("TTS environment did not override YAML: %+v", cfg.TTS)
	}
	if cfg.Audio.OutputDevice != "pulse" {
		t.Fatalf("Audio.OutputDevice = %q", cfg.Audio.OutputDevice)
	}

}

func TestLoadTTSYamlValuesWithoutEnvironment(t *testing.T) {
	unsetEnv(t, "ASSISTANT_TTS_MODEL_DIR")
	unsetEnv(t, "ASSISTANT_TTS_THREADS")
	unsetEnv(t, "ASSISTANT_TTS_TIMEOUT")
	path := writeConfig(t, `
tts:
  model_dir: /models/from-file
  threads: 4
  timeout: 3m
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TTS.ModelDir != "/models/from-file" || cfg.TTS.Threads != 4 || cfg.TTS.Timeout.Std() != 3*time.Minute {
		t.Fatalf("TTS YAML values = %+v", cfg.TTS)
	}
}

func TestLoadRejectsInvalidTTSEnvironmentTypes(t *testing.T) {
	for _, test := range []struct{ name, value string }{
		{"ASSISTANT_TTS_THREADS", "many"},
		{"ASSISTANT_TTS_TIMEOUT", "later"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(test.name, test.value)
			if _, err := Load(""); err == nil || !strings.Contains(err.Error(), test.name) {
				t.Fatalf("Load() error=%v, want %s parse error", err, test.name)
			}
		})
	}
}

func TestLoadDoesNotValidateNativeTTSOptions(t *testing.T) {
	t.Setenv("ASSISTANT_TTS_MODEL_DIR", "")
	t.Setenv("ASSISTANT_TTS_THREADS", "0")
	t.Setenv("ASSISTANT_TTS_TIMEOUT", "0s")
	if _, err := Load(""); err != nil {
		t.Fatalf("Load() performed native TTS validation: %v", err)
	}
}

func TestLoadYAMLAndEnvironmentPrecedence(t *testing.T) {
	path := writeConfig(t, `
llm:
  model: from-file
  library_dir: /from-file/native
  context_size: 2048
  gpu_layers: 12
  threads: 3
  timeout: 22s
  temperature: 0.7
vad:
  end_silence: 750ms
`)
	t.Setenv("ASSISTANT_LLM_MODEL", "from-env")
	t.Setenv("ASSISTANT_LLM_LIBRARY_DIR", "/from-env/native")

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
	if cfg.LLM.LibraryDir != "/from-env/native" || cfg.LLM.ContextSize != 2048 || cfg.LLM.GPULayers != 12 || cfg.LLM.Threads != 3 || cfg.LLM.Timeout.Std() != 22*time.Second {
		t.Fatalf("LLM YAML/environment values not applied: %+v", cfg.LLM)
	}
}

func TestLoadRejectsInvalidLLMEnvironmentTypes(t *testing.T) {
	for _, test := range []struct{ name, value string }{
		{"ASSISTANT_LLM_THREADS", "many"},
		{"ASSISTANT_LLM_CONTEXT_SIZE", "large"},
		{"ASSISTANT_LLM_TIMEOUT", "soon"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(test.name, test.value)
			if _, err := Load(""); err == nil || !strings.Contains(err.Error(), test.name) {
				t.Fatalf("Load() error=%v, want %s parse error", err, test.name)
			}
		})
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

func unsetEnv(t *testing.T, name string) {
	t.Helper()
	value, wasSet := os.LookupEnv(name)
	if err := os.Unsetenv(name); err != nil {
		t.Fatalf("Unsetenv(%q): %v", name, err)
	}
	t.Cleanup(func() {
		if wasSet {
			_ = os.Setenv(name, value)
		} else {
			_ = os.Unsetenv(name)
		}
	})
}

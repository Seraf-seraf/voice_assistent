//go:build tts_integration && linux && amd64 && cgo && !android && !musl

package sherpa

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Seraf-seraf/voice_assistent/internal/audio"
	"github.com/Seraf-seraf/voice_assistent/internal/tts"
)

func TestPinnedModelNativeSynthesis(t *testing.T) {
	modelDir := os.Getenv("ASSISTANT_TTS_MODEL_DIR")
	if modelDir == "" {
		t.Fatal("ASSISTANT_TTS_MODEL_DIR обязателен для tts_integration")
	}
	modelPath := filepath.Join(modelDir, modelFileName)
	before, err := hashFile(modelPath)
	if err != nil {
		t.Fatal(err)
	}
	if before != modelSHA256 {
		t.Fatalf("model SHA-256 = %s, want pinned %s", before, modelSHA256)
	}
	engine, err := Open(context.Background(), Options{ModelDir: modelDir, Threads: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Добрый день. Проверяю локальный синтез речи.", stringsForRunes(200)} {
		pcm, err := engine.Synthesize(context.Background(), text)
		if err != nil {
			t.Fatalf("Synthesize(%d runes): %v", utf8.RuneCountInString(text), err)
		}
		validateNativePCM(t, pcm)
	}
	if _, err := engine.Synthesize(context.Background(), stringsForRunes(201)); !errors.Is(err, tts.ErrInvalidText) {
		t.Fatalf("201-rune synthesis error = %v, want ErrInvalidText", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := engine.Synthesize(canceled, "отменённый запрос"); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled synthesis error = %v", err)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := hashFile(modelPath)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("model SHA-256 changed: before=%s after=%s", before, after)
	}
}

func validateNativePCM(t *testing.T, pcm audio.PCM) {
	t.Helper()
	if err := pcm.Validate(); err != nil {
		t.Fatal(err)
	}
	if pcm.SampleRate != ttsSampleRate {
		t.Fatalf("sample rate = %d, want %d", pcm.SampleRate, ttsSampleRate)
	}
	for i, sample := range pcm.Samples {
		if math.IsNaN(float64(sample)) || math.IsInf(float64(sample), 0) {
			t.Fatalf("sample %d is not finite", i)
		}
	}
}

func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

func stringsForRunes(count int) string {
	return strings.Repeat("я", count)
}

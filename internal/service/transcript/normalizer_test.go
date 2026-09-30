package transcript

import (
	"errors"
	"testing"
)

func TestNormalizerPreservesTextAndBuildsCanonicalForm(t *testing.T) {
	normalizer := newTestNormalizer(t, Options{MinSignificantRunes: 2})

	result, err := normalizer.Normalize("  Ёлка,\n  ЗЕЛЁНАЯ!  ")
	if err != nil {
		t.Fatalf("Normalize() error: %v", err)
	}
	if result.Text != "Ёлка, ЗЕЛЁНАЯ!" {
		t.Fatalf("Text = %q", result.Text)
	}
	if result.Canonical != "елка, зеленая" {
		t.Fatalf("Canonical = %q", result.Canonical)
	}
}

func TestNormalizerRejectsEmptyAndShortText(t *testing.T) {
	normalizer := newTestNormalizer(t, Options{MinSignificantRunes: 2})
	if _, err := normalizer.Normalize(" \n\t "); !errors.Is(err, ErrEmpty) {
		t.Fatalf("Normalize(empty) error = %v, want ErrEmpty", err)
	}
	if _, err := normalizer.Normalize("а!"); !errors.Is(err, ErrTooShort) {
		t.Fatalf("Normalize(short) error = %v, want ErrTooShort", err)
	}
}

func TestNormalizerFiltersExactPhraseAfterCanonicalization(t *testing.T) {
	normalizer := newTestNormalizer(t, Options{
		MinSignificantRunes: 2,
		IgnoredExact:        []string{"ЭЭ"},
	})
	if _, err := normalizer.Normalize("ээ..."); !errors.Is(err, ErrFiltered) {
		t.Fatalf("Normalize() error = %v, want ErrFiltered", err)
	}
}

func TestNormalizerFiltersPattern(t *testing.T) {
	normalizer := newTestNormalizer(t, Options{
		MinSignificantRunes: 2,
		IgnoredPatterns:     []string{`^субтитры (сделал|создал).*$`},
	})
	if _, err := normalizer.Normalize("Субтитры сделал Вася"); !errors.Is(err, ErrFiltered) {
		t.Fatalf("Normalize() error = %v, want ErrFiltered", err)
	}
}

func TestNewNormalizerRejectsInvalidOptions(t *testing.T) {
	if _, err := NewNormalizer(Options{}); err == nil {
		t.Fatal("NewNormalizer() accepted zero minimum")
	}
	if _, err := NewNormalizer(Options{MinSignificantRunes: 2, IgnoredExact: []string{" "}}); err == nil {
		t.Fatal("NewNormalizer() accepted empty exact phrase")
	}
	if _, err := NewNormalizer(Options{MinSignificantRunes: 2, IgnoredPatterns: []string{"["}}); err == nil {
		t.Fatal("NewNormalizer() accepted invalid regexp")
	}
}

func newTestNormalizer(t *testing.T, options Options) *Normalizer {
	t.Helper()
	normalizer, err := NewNormalizer(options)
	if err != nil {
		t.Fatalf("NewNormalizer() error: %v", err)
	}
	return normalizer
}

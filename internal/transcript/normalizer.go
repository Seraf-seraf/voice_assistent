package transcript

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

var (
	ErrEmpty    = errors.New("транскрипт пуст")
	ErrTooShort = errors.New("транскрипт слишком короткий")
	ErrFiltered = errors.New("транскрипт отфильтрован")
)

type Options struct {
	MinSignificantRunes int
	IgnoredExact        []string
	IgnoredPatterns     []string
}

type Result struct {
	Text      string
	Canonical string
}

type Normalizer struct {
	minSignificantRunes int
	ignoredExact        map[string]struct{}
	ignoredPatterns     []*regexp.Regexp
}

func NewNormalizer(options Options) (*Normalizer, error) {
	if options.MinSignificantRunes <= 0 {
		return nil, errors.New("минимальное число значимых рун должно быть положительным")
	}
	normalizer := &Normalizer{
		minSignificantRunes: options.MinSignificantRunes,
		ignoredExact:        make(map[string]struct{}, len(options.IgnoredExact)),
		ignoredPatterns:     make([]*regexp.Regexp, 0, len(options.IgnoredPatterns)),
	}
	for _, value := range options.IgnoredExact {
		canonical := Canonicalize(value)
		if canonical == "" {
			return nil, errors.New("точная фраза для игнорирования не может быть пустой")
		}
		normalizer.ignoredExact[canonical] = struct{}{}
	}
	for _, pattern := range options.IgnoredPatterns {
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("скомпилировать шаблон для игнорирования %q: %w", pattern, err)
		}
		normalizer.ignoredPatterns = append(normalizer.ignoredPatterns, compiled)
	}
	return normalizer, nil
}

func (n *Normalizer) Normalize(raw string) (Result, error) {
	text := strings.Join(strings.Fields(raw), " ")
	if text == "" {
		return Result{}, ErrEmpty
	}
	canonical := Canonicalize(text)
	if _, ignored := n.ignoredExact[canonical]; ignored {
		return Result{}, ErrFiltered
	}
	for _, pattern := range n.ignoredPatterns {
		if pattern.MatchString(canonical) {
			return Result{}, ErrFiltered
		}
	}
	if significantRunes(canonical) < n.minSignificantRunes {
		return Result{}, ErrTooShort
	}
	return Result{Text: text, Canonical: canonical}, nil
}

// Canonicalize создаёт строку для детерминированного сравнения команд.
func Canonicalize(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, "ё", "е")
	return strings.TrimFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r)
	})
}

func significantRunes(value string) int {
	count := 0
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			count++
		}
	}
	return count
}

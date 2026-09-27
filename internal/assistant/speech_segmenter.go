package assistant

import (
	"strings"
	"unicode"

	"github.com/Seraf-seraf/voice_assistent/internal/tts"
)

type speechSegmenter struct {
	pending []rune
}

func (s *speechSegmenter) Push(text string) []string {
	var completed []string
	for _, r := range text {
		s.pending = append(s.pending, r)
		if r == '\n' || (unicode.IsSpace(r) && hasSpeechBoundary(s.pending)) {
			completed = append(completed, string(s.pending))
			s.pending = s.pending[:0]
			continue
		}
		if len(s.pending) == tts.MaxTextRunes {
			cut := len(s.pending)
			for i := len(s.pending) - 1; i >= 0; i-- {
				if unicode.IsSpace(s.pending[i]) {
					cut = i + 1
					break
				}
			}
			completed = append(completed, string(s.pending[:cut]))
			remaining := append([]rune(nil), s.pending[cut:]...)
			s.pending = remaining
		}
	}
	return completed
}

func (s *speechSegmenter) Finish() string {
	finished := string(s.pending)
	s.pending = nil
	return finished
}

func hasSpeechBoundary(pending []rune) bool {
	index := len(pending) - 2 // Вызывающий код уже добавил пробел, завершающий фразу.
	for index >= 0 && isSpeechClosingRune(pending[index]) {
		index--
	}
	sawPunctuation := false
	for index >= 0 && isSpeechPunctuation(pending[index]) {
		sawPunctuation = true
		index--
	}
	return sawPunctuation
}

func isSpeechClosingRune(r rune) bool {
	return strings.ContainsRune("\"'»”)]", r)
}

func isSpeechPunctuation(r rune) bool {
	return strings.ContainsRune(".!?;:", r)
}

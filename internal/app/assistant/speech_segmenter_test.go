package assistant

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func collectSpeechSegments(chunks []string) ([]string, string) {
	segmenter := &speechSegmenter{}
	var segments []string
	var reconstructed strings.Builder
	for _, chunk := range chunks {
		for _, segment := range segmenter.Push(chunk) {
			segments = append(segments, segment)
			reconstructed.WriteString(segment)
		}
	}
	tail := segmenter.Finish()
	reconstructed.WriteString(tail)
	if tail != "" {
		segments = append(segments, tail)
	}
	return segments, reconstructed.String()
}

func TestSpeechSegmenterChunkBoundariesDoNotChangeSegments(t *testing.T) {
	input := "Привет. Как дела? Всё хорошо!"
	want := []string{"Привет. ", "Как дела? ", "Всё хорошо!"}
	chunkings := [][]string{{input}}
	runes := []rune(input)
	oneRuneChunks := make([]string, len(runes))
	for i, r := range runes {
		oneRuneChunks[i] = string(r)
	}
	chunkings = append(chunkings, oneRuneChunks)
	for split := 0; split <= len(runes); split++ {
		chunkings = append(chunkings, []string{string(runes[:split]), string(runes[split:])})
	}
	for i, chunks := range chunkings {
		got, reconstructed := collectSpeechSegments(chunks)
		if reconstructed != input {
			t.Fatalf("chunking %d reconstruction = %q, want %q", i, reconstructed, input)
		}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("chunking %d segments = %#v, want %#v", i, got, want)
		}
	}
}

func TestSpeechSegmenterPunctuationAndNewlines(t *testing.T) {
	for _, test := range []struct {
		input string
		want  []string
	}{
		{input: "3.14 is pi. Next", want: []string{"3.14 is pi. ", "Next"}},
		{input: "Wait... Really?! Yes.", want: []string{"Wait... ", "Really?! ", "Yes."}},
		{input: "Он сказал: «да»! Потом", want: []string{"Он сказал: ", "«да»! ", "Потом"}},
		{input: "Первая\r\nВторая", want: []string{"Первая\r\n", "Вторая"}},
	} {
		segments, reconstructed := collectSpeechSegments([]string{test.input})
		if reconstructed != test.input || strings.Join(segments, "|") != strings.Join(test.want, "|") {
			t.Errorf("input %q: segments=%#v reconstruction=%q", test.input, segments, reconstructed)
		}
	}
}

func TestSpeechSegmenterLengthAndFinish(t *testing.T) {
	for _, test := range []struct {
		name          string
		input         string
		wantRuneSizes []int
	}{
		{name: "199 runes", input: strings.Repeat("я", 199), wantRuneSizes: []int{199}},
		{name: "200 runes", input: strings.Repeat("я", 200), wantRuneSizes: []int{200}},
		{name: "201 runes", input: strings.Repeat("я", 201), wantRuneSizes: []int{200, 1}},
		{name: "split at last whitespace", input: strings.Repeat("я", 190) + " " + strings.Repeat("я", 10), wantRuneSizes: []int{191, 10}},
	} {
		t.Run(test.name, func(t *testing.T) {
			segments, reconstructed := collectSpeechSegments([]string{test.input})
			if reconstructed != test.input {
				t.Fatalf("reconstructed %d runes, want %d", utf8.RuneCountInString(reconstructed), utf8.RuneCountInString(test.input))
			}
			if len(segments) != len(test.wantRuneSizes) {
				t.Fatalf("got %d segments, want %d: %#v", len(segments), len(test.wantRuneSizes), segments)
			}
			for i, segment := range segments {
				count := utf8.RuneCountInString(segment)
				if count > 200 {
					t.Fatalf("segment has %d runes", count)
				}
				if count != test.wantRuneSizes[i] {
					t.Fatalf("segment %d has %d runes, want %d", i, count, test.wantRuneSizes[i])
				}
			}
		})
	}
	segmenter := &speechSegmenter{}
	if got := segmenter.Push("остаток"); len(got) != 0 {
		t.Fatalf("Push() = %#v, want no completed segment", got)
	}
	if got := segmenter.Finish(); got != "остаток" {
		t.Fatalf("Finish() = %q", got)
	}
	if got := segmenter.Finish(); got != "" {
		t.Fatalf("second Finish() = %q", got)
	}
}

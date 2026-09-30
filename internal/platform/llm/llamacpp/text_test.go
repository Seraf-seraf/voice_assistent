package llamacpp

import (
	"errors"
	"strings"
	"testing"

	"github.com/Seraf-seraf/voice_assistent/internal/service/llm"
)

func TestTextDecoderHandlesSplitUTF8AndPreservesSpaces(t *testing.T) {
	var decoder textDecoder
	var got []string
	emit := func(delta llm.TextDelta) error { got = append(got, delta.Text); return nil }
	for _, piece := range [][]byte{[]byte(" При"), []byte("в"), []byte{0xd0}, []byte{0xb5}, []byte("т, "), nil, []byte("мир! ")} {
		if err := decoder.Push(piece, emit); err != nil {
			t.Fatal(err)
		}
	}
	if err := decoder.Finish(emit); err != nil {
		t.Fatal(err)
	}
	if gotText := strings.Join(got, ""); gotText != " Привет, мир! " {
		t.Fatalf("text=%q", gotText)
	}
}

func TestTextDecoderRejectsMalformedAndIncompleteUTF8(t *testing.T) {
	for _, pieces := range [][][]byte{{{0xff}}, {{0xe2}, {0x82}}} {
		var decoder textDecoder
		for _, piece := range pieces {
			if err := decoder.Push(piece, func(llm.TextDelta) error { return nil }); err != nil {
				if !errors.Is(err, ErrInvalidOutput) {
					t.Fatal(err)
				}
				continue
			}
		}
		if err := decoder.Finish(func(llm.TextDelta) error { return nil }); !errors.Is(err, ErrInvalidOutput) {
			t.Fatalf("Finish()=%v", err)
		}
	}
}

func TestTextDecoderBlocksSpecialMarkersAcrossPieces(t *testing.T) {
	for _, pieces := range [][][]byte{{[]byte("x<think>")}, {[]byte("x</think>")}, {[]byte("x<th"), []byte("ink>")}, {[]byte("<|im"), []byte("_start|>")}} {
		var decoder textDecoder
		for _, piece := range pieces {
			err := decoder.Push(piece, func(llm.TextDelta) error { return nil })
			if errors.Is(err, ErrInvalidOutput) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := decoder.Finish(func(llm.TextDelta) error { return nil }); !errors.Is(err, ErrInvalidOutput) {
			t.Fatalf("marker accepted: %q", pieces)
		}
	}
	var decoder textDecoder
	var got []string
	if err := decoder.Push([]byte("</<th"), func(d llm.TextDelta) error { got = append(got, d.Text); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Finish(func(d llm.TextDelta) error { got = append(got, d.Text); return nil }); err != nil {
		t.Fatal(err)
	}
	if gotText := strings.Join(got, ""); gotText != "</<th" {
		t.Fatalf("got=%q", gotText)
	}
}

func TestTextDecoderStopsAfterEmitError(t *testing.T) {
	var decoder textDecoder
	sentinel := errors.New("emit failed")
	calls := 0
	err := decoder.Push([]byte("hello world"), func(llm.TextDelta) error { calls++; return sentinel })
	if !errors.Is(err, sentinel) || calls != 1 {
		t.Fatalf("Push()=%v calls=%d", err, calls)
	}
}

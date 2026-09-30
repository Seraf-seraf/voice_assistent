package tts

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestValidateText(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		wantErr bool
	}{
		{name: "199 runes", text: strings.Repeat("я", 199)},
		{name: "200 runes", text: strings.Repeat("я", 200)},
		{name: "201 runes", text: strings.Repeat("я", 201), wantErr: true},
		{name: "multibyte runes", text: strings.Repeat("ё", 200)},
		{name: "empty", text: "", wantErr: true},
		{name: "whitespace", text: " \n\t", wantErr: true},
		{name: "nul", text: "привет\x00мир", wantErr: true},
		{name: "invalid utf8", text: string([]byte{0xff}), wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			original := test.text
			err := ValidateText(test.text)
			if test.wantErr {
				if !errors.Is(err, ErrInvalidText) {
					t.Fatalf("ValidateText() error = %v, want ErrInvalidText", err)
				}
			} else if err != nil {
				t.Fatalf("ValidateText() error = %v", err)
			}
			if test.text != original || (utf8.ValidString(test.text) && utf8.RuneCountInString(test.text) != utf8.RuneCountInString(original)) {
				t.Fatal("ValidateText mutated input")
			}
		})
	}
}

package llm

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func TestOptionsValidate(t *testing.T) {
	temperatureCases := []struct {
		name    string
		value   float64
		wantErr bool
	}{
		{name: "below minimum", value: -0.01, wantErr: true},
		{name: "minimum", value: 0},
		{name: "typical", value: 0.4},
		{name: "maximum", value: 2},
		{name: "above maximum", value: 2.01, wantErr: true},
		{name: "NaN", value: math.NaN(), wantErr: true},
		{name: "positive infinity", value: math.Inf(1), wantErr: true},
		{name: "negative infinity", value: math.Inf(-1), wantErr: true},
	}
	for _, test := range temperatureCases {
		t.Run("temperature/"+test.name, func(t *testing.T) {
			err := (Options{Temperature: test.value, MaxTokens: 512}).Validate()
			if test.wantErr {
				if !errors.Is(err, ErrInvalidOptions) {
					t.Fatalf("Validate() error = %v, want ErrInvalidOptions", err)
				}
				if !strings.Contains(err.Error(), "Temperature") {
					t.Fatalf("Validate() error = %v, want parameter name", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}

	tokenCases := []struct {
		name    string
		value   int
		wantErr bool
	}{
		{name: "negative", value: -1, wantErr: true},
		{name: "zero", value: 0, wantErr: true},
		{name: "one", value: 1},
		{name: "typical", value: 512},
	}
	for _, test := range tokenCases {
		t.Run("max_tokens/"+test.name, func(t *testing.T) {
			err := (Options{Temperature: 0.4, MaxTokens: test.value}).Validate()
			if test.wantErr {
				if !errors.Is(err, ErrInvalidOptions) {
					t.Fatalf("Validate() error = %v, want ErrInvalidOptions", err)
				}
				if !strings.Contains(err.Error(), "MaxTokens") {
					t.Fatalf("Validate() error = %v, want parameter name", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestOptionsValidateChecksTemperatureBeforeMaxTokens(t *testing.T) {
	err := (Options{Temperature: 3, MaxTokens: 0}).Validate()
	if !errors.Is(err, ErrInvalidOptions) || !strings.Contains(err.Error(), "Temperature") {
		t.Fatalf("Validate() error = %v, want Temperature validation first", err)
	}
}

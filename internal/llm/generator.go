package llm

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/Seraf-seraf/voice_assistent/internal/dialogue"
)

var ErrInvalidOptions = errors.New("некорректные параметры генерации")

type Options struct {
	Temperature float64
	MaxTokens   int
}

func (o Options) Validate() error {
	if math.IsNaN(o.Temperature) || math.IsInf(o.Temperature, 0) || o.Temperature < 0 || o.Temperature > 2 {
		return fmt.Errorf("%w: Temperature должна быть конечной и находиться в диапазоне [0, 2]", ErrInvalidOptions)
	}
	if o.MaxTokens <= 0 {
		return fmt.Errorf("%w: MaxTokens должен быть положительным", ErrInvalidOptions)
	}
	return nil
}

type Request struct {
	Dialogue dialogue.Snapshot
	Options  Options
}

type TextDelta struct {
	Text string
}

type Emit func(TextDelta) error

type Generator interface {
	Generate(ctx context.Context, request Request, emit Emit) error
}

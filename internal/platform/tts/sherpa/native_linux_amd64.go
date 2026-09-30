//go:build linux && amd64 && cgo && !android && !musl

package sherpa

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/Seraf-seraf/voice_assistent/internal/service/audio"
	sherpa "github.com/k2-fsa/sherpa-onnx-go-linux"
)

func Open(ctx context.Context, options Options) (*Engine, error) {
	return openEngine(ctx, options)
}

func openNativeBackend(ctx context.Context, options Options) (backend, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	directory := options.ModelDir
	cfg := sherpa.OfflineTtsConfig{
		Model: sherpa.OfflineTtsModelConfig{
			Vits: sherpa.OfflineTtsVitsModelConfig{
				Model:       filepath.Join(directory, modelFileName),
				Tokens:      filepath.Join(directory, tokensFileName),
				DataDir:     filepath.Join(directory, espeakDataDirName),
				NoiseScale:  0.667,
				NoiseScaleW: 0.8,
				LengthScale: 1,
			},
			NumThreads: options.Threads,
			Provider:   "cpu",
			Debug:      0,
		},
		MaxNumSentences: 1,
		SilenceScale:    1,
	}
	engine := sherpa.NewOfflineTts(&cfg)
	if engine == nil {
		return nil, fmt.Errorf("%w: sherpa не создал движок синтеза", ErrUnavailable)
	}
	return &nativeBackend{engine: engine}, nil
}

type nativeBackend struct {
	engine *sherpa.OfflineTts
}

func (b *nativeBackend) synthesize(ctx context.Context, text string) (audio.PCM, error) {
	if err := ctx.Err(); err != nil {
		return audio.PCM{}, err
	}
	generated := b.engine.GenerateWithConfig(text, &sherpa.GenerationConfig{Sid: 0, Speed: 1, SilenceScale: 1}, func([]float32, float32) bool {
		return ctx.Err() == nil
	})
	if err := ctx.Err(); err != nil {
		return audio.PCM{}, err
	}
	if generated == nil {
		return audio.PCM{}, fmt.Errorf("sherpa вернул пустой результат синтеза")
	}
	return audio.PCM{Samples: generated.Samples, SampleRate: generated.SampleRate}, nil
}

func (b *nativeBackend) close() error {
	if b.engine == nil {
		return nil
	}
	sherpa.DeleteOfflineTts(b.engine)
	b.engine = nil
	return nil
}

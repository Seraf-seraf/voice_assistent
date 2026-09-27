package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"

	"github.com/Seraf-seraf/voice_assistent/internal/assistant"
	"github.com/Seraf-seraf/voice_assistent/internal/audio/output"
	"github.com/Seraf-seraf/voice_assistent/internal/audio/output/alsa"
	"github.com/Seraf-seraf/voice_assistent/internal/config"
	"github.com/Seraf-seraf/voice_assistent/internal/tts"
	"github.com/Seraf-seraf/voice_assistent/internal/tts/sherpa"
)

const speechSampleRate = 22050

type closeableSpeechSynthesizer interface {
	tts.Synthesizer
	Close() error
}

type closeableSpeechPlayer interface {
	output.Player
	Close() error
}

func newSpeechOutput(ctx context.Context, cfg config.TTSConfig, device string, textSink assistant.ResponseSink) (*assistant.SpokenResponseSink, func() error, error) {
	return newSpeechOutputWithFactories(
		ctx,
		cfg,
		device,
		textSink,
		func(ctx context.Context, options sherpa.Options) (closeableSpeechSynthesizer, error) {
			return sherpa.Open(ctx, options)
		},
		func(ctx context.Context, options alsa.Options) (closeableSpeechPlayer, error) {
			return alsa.Open(ctx, options)
		},
	)
}

func newSpeechOutputWithFactories(
	ctx context.Context,
	cfg config.TTSConfig,
	device string,
	textSink assistant.ResponseSink,
	openSynthesizer func(context.Context, sherpa.Options) (closeableSpeechSynthesizer, error),
	openPlayer func(context.Context, alsa.Options) (closeableSpeechPlayer, error),
) (*assistant.SpokenResponseSink, func() error, error) {
	if cfg.ModelDir == "" {
		return nil, nil, errors.New("не указан каталог модели TTS")
	}
	speechOptions := assistant.SpeechOptions{Timeout: cfg.Timeout.Std()}
	if err := speechOptions.Validate(); err != nil {
		return nil, nil, err
	}
	synthOptions := sherpa.Options{ModelDir: cfg.ModelDir, Threads: cfg.Threads}
	if err := synthOptions.Validate(); err != nil {
		return nil, nil, err
	}
	playerOptions := alsa.Options{Device: device, SampleRate: speechSampleRate}
	if err := playerOptions.Validate(); err != nil {
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	synthesizer, err := openSynthesizer(ctx, synthOptions)
	if err != nil {
		return nil, nil, fmt.Errorf("открыть sherpa TTS: %w", err)
	}
	if synthesizer == nil || isNil(synthesizer) {
		return nil, nil, errors.New("sherpa TTS вернул пустой синтезатор")
	}
	player, err := openPlayer(ctx, playerOptions)
	if err != nil {
		return nil, nil, errors.Join(fmt.Errorf("открыть аудиовыход ALSA: %w", err), closeSpeechSynthesizer(synthesizer))
	}
	if player == nil || isNil(player) {
		return nil, nil, errors.Join(errors.New("выход ALSA вернул пустой проигрыватель"), closeSpeechPlayer(player), closeSpeechSynthesizer(synthesizer))
	}
	spoke, err := assistant.NewSpokenResponseSink(textSink, synthesizer, player, speechOptions)
	if err != nil {
		return nil, nil, errors.Join(err, closeSpeechPlayer(player), closeSpeechSynthesizer(synthesizer))
	}
	var cleanupOnce sync.Once
	var cleanupErr error
	cleanup := func() error {
		cleanupOnce.Do(func() {
			cleanupErr = errors.Join(closeSpeechPlayer(player), closeSpeechSynthesizer(synthesizer))
		})
		return cleanupErr
	}
	return spoke, cleanup, nil
}

func closeSpeechPlayer(player closeableSpeechPlayer) error {
	if player == nil || isNil(player) {
		return nil
	}
	if err := player.Close(); err != nil {
		return fmt.Errorf("закрыть аудиовыход ALSA: %w", err)
	}
	return nil
}

func closeSpeechSynthesizer(synthesizer closeableSpeechSynthesizer) error {
	if synthesizer == nil || isNil(synthesizer) {
		return nil
	}
	if err := synthesizer.Close(); err != nil {
		return fmt.Errorf("закрыть синтезатор sherpa TTS: %w", err)
	}
	return nil
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

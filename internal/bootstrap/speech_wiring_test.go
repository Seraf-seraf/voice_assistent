package bootstrap

import (
	"context"
	"errors"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/app/assistant"
	"github.com/Seraf-seraf/voice_assistent/internal/bootstrap/config"
	"github.com/Seraf-seraf/voice_assistent/internal/platform/tts/sherpa"
	"github.com/Seraf-seraf/voice_assistent/internal/service/audio"
)

type speechWiringSynth struct {
	events   *[]string
	closeErr error
}

func (s *speechWiringSynth) Synthesize(context.Context, string) (audio.PCM, error) {
	return audio.PCM{Samples: []float32{0}, SampleRate: 22050}, nil
}

func (s *speechWiringSynth) Close() error {
	*s.events = append(*s.events, "close synth")
	return s.closeErr
}

type speechWiringPlayer struct {
	events   *[]string
	closeErr error
}

func (p *speechWiringPlayer) Play(context.Context, audio.PCM) error { return nil }
func (p *speechWiringPlayer) Close() error {
	*p.events = append(*p.events, "close player")
	return p.closeErr
}

func TestNewSpeechOutputWithFactoriesAcquiresAndReleasesInOrder(t *testing.T) {
	var events []string
	synth := &speechWiringSynth{events: &events}
	player := &speechWiringPlayer{events: &events}
	textSink, err := newResponseOutput(io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	openSynth := func(_ context.Context, options sherpa.Options) (closeableSpeechSynthesizer, error) {
		if options.ModelDir != "/models/ruslan" || options.Threads != 2 {
			t.Fatalf("sherpa options = %+v", options)
		}
		events = append(events, "open synth")
		return synth, nil
	}
	openPlayer := func(_ context.Context, options speechPlayerOptions) (closeableSpeechPlayer, error) {
		if options.Device != "pulse" || options.SampleRate != 22050 {
			t.Fatalf("player options = %+v", options)
		}
		events = append(events, "open player")
		return player, nil
	}
	spoke, cleanup, err := newSpeechOutputWithFactories(context.Background(), config.TTSConfig{
		ModelDir: "/models/ruslan", Threads: 2, Timeout: config.Duration(time.Minute),
	}, "pulse", textSink, openSynth, openPlayer)
	if err != nil {
		t.Fatal(err)
	}
	if spoke == nil {
		t.Fatal("speech sink is nil")
	}
	var _ *assistant.SpokenResponseSink = spoke
	if !reflect.DeepEqual(events, []string{"open synth", "open player"}) {
		t.Fatalf("acquisition order = %v", events)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []string{"open synth", "open player", "close player", "close synth"}) {
		t.Fatalf("acquisition/release order = %v", events)
	}
}

func TestNewSpeechOutputClosesSynthWhenPlayerOpenFails(t *testing.T) {
	var events []string
	synthCloseErr := errors.New("synth cleanup failed")
	synth := &speechWiringSynth{events: &events, closeErr: synthCloseErr}
	openSynth := func(context.Context, sherpa.Options) (closeableSpeechSynthesizer, error) {
		events = append(events, "open synth")
		return synth, nil
	}
	playerErr := errors.New("selected device unavailable")
	openPlayer := func(context.Context, speechPlayerOptions) (closeableSpeechPlayer, error) {
		events = append(events, "open player")
		return nil, playerErr
	}
	textSink, _ := newResponseOutput(io.Discard)
	_, cleanup, err := newSpeechOutputWithFactories(context.Background(), config.TTSConfig{
		ModelDir: "/models/ruslan", Threads: 2, Timeout: config.Duration(time.Minute),
	}, "bad-device", textSink, openSynth, openPlayer)
	if !errors.Is(err, playerErr) || !errors.Is(err, synthCloseErr) || cleanup != nil {
		t.Fatalf("factory error=%v cleanup returned=%t", err, cleanup != nil)
	}
	if !reflect.DeepEqual(events, []string{"open synth", "open player", "close synth"}) {
		t.Fatalf("startup cleanup order = %v", events)
	}
}

func TestSpeechOutputCleanupPreservesBothErrors(t *testing.T) {
	var events []string
	synthErr := errors.New("close synth")
	playerErr := errors.New("close player")
	synth := &speechWiringSynth{events: &events, closeErr: synthErr}
	player := &speechWiringPlayer{events: &events, closeErr: playerErr}
	openSynth := func(context.Context, sherpa.Options) (closeableSpeechSynthesizer, error) {
		return synth, nil
	}
	openPlayer := func(context.Context, speechPlayerOptions) (closeableSpeechPlayer, error) {
		return player, nil
	}
	textSink, _ := newResponseOutput(io.Discard)
	_, cleanup, err := newSpeechOutputWithFactories(context.Background(), config.TTSConfig{
		ModelDir: "/models/ruslan", Threads: 2, Timeout: config.Duration(time.Minute),
	}, "default", textSink, openSynth, openPlayer)
	if err != nil {
		t.Fatal(err)
	}
	err = cleanup()
	if !errors.Is(err, synthErr) || !errors.Is(err, playerErr) {
		t.Fatalf("cleanup error = %v", err)
	}
}

func TestNewSpeechOutputValidatesBeforeAcquiringResources(t *testing.T) {
	called := false
	openSynth := func(context.Context, sherpa.Options) (closeableSpeechSynthesizer, error) {
		called = true
		return nil, nil
	}
	openPlayer := func(context.Context, speechPlayerOptions) (closeableSpeechPlayer, error) {
		called = true
		return nil, nil
	}
	textSink, _ := newResponseOutput(io.Discard)
	_, _, err := newSpeechOutputWithFactories(context.Background(), config.TTSConfig{
		ModelDir: "", Threads: 0, Timeout: 0,
	}, "bad\x00device", textSink, openSynth, openPlayer)
	if err == nil || called {
		t.Fatalf("factory error=%v factories called=%t", err, called)
	}
}

func TestDisabledSpeechFactoryDoesNotAcquireResources(t *testing.T) {
	called := false
	openSynth := func(context.Context, sherpa.Options) (closeableSpeechSynthesizer, error) {
		called = true
		return nil, nil
	}
	openPlayer := func(context.Context, speechPlayerOptions) (closeableSpeechPlayer, error) {
		called = true
		return nil, nil
	}
	textSink, _ := newResponseOutput(io.Discard)
	_, _, err := newSpeechOutputWithFactories(context.Background(), config.TTSConfig{}, "bad\x00device", textSink, openSynth, openPlayer)
	if err == nil || called {
		t.Fatalf("factory error=%v resource factory called=%t", err, called)
	}
}

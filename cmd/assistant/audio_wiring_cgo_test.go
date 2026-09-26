//go:build cgo

package main

import (
	"testing"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/audio"
	"github.com/Seraf-seraf/voice_assistent/internal/config"
)

func TestNewAudioInputComponents(t *testing.T) {
	audioCfg := config.Default().Audio
	format := newAudioFormat(audioCfg)
	vadComponents, err := newVADComponents(format, config.Default().VAD)
	if err != nil {
		t.Fatalf("newVADComponents() error: %v", err)
	}
	defer func() {
		if err := vadComponents.detector.Close(); err != nil {
			t.Errorf("detector.Close() error: %v", err)
		}
	}()

	components, err := newAudioInputComponents(format, audioCfg, vadComponents)
	if err != nil {
		t.Fatalf("newAudioInputComponents() error: %v", err)
	}
	if components.source == nil {
		t.Fatal("source is nil")
	}
	if components.listener == nil {
		t.Fatal("listener is nil")
	}
	if format != (audio.Format{SampleRate: 16000, Channels: 1, FrameDuration: 20 * time.Millisecond}) {
		t.Fatalf("unexpected canonical format: %+v", format)
	}

	if err := components.source.Close(); err != nil {
		t.Fatalf("source.Close() error: %v", err)
	}
}

func TestNewAudioInputComponentsRejectsInvalidQueueSize(t *testing.T) {
	audioCfg := config.Default().Audio
	audioCfg.BufferFrames = 0
	format := newAudioFormat(audioCfg)
	vadComponents, err := newVADComponents(format, config.Default().VAD)
	if err != nil {
		t.Fatalf("newVADComponents() error: %v", err)
	}
	defer func() {
		if err := vadComponents.detector.Close(); err != nil {
			t.Errorf("detector.Close() error: %v", err)
		}
	}()

	if _, err := newAudioInputComponents(format, audioCfg, vadComponents); err == nil {
		t.Fatal("newAudioInputComponents() succeeded, want source creation error")
	}
}

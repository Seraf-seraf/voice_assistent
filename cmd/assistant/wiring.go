package main

import (
	"fmt"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/audio"
	"github.com/Seraf-seraf/voice_assistent/internal/config"
	"github.com/Seraf-seraf/voice_assistent/internal/vad"
)

type vadComponents struct {
	detector  vad.Detector
	segmenter *vad.Segmenter
}

func newVADComponents(
	audioCfg config.AudioConfig,
	vadCfg config.VADConfig,
) (vadComponents, error) {
	format := audio.Format{
		SampleRate:    audioCfg.SampleRate,
		Channels:      audioCfg.Channels,
		FrameDuration: time.Duration(audioCfg.FrameMS) * time.Millisecond,
	}

	detector, err := vad.NewWebRTCDetector(format, vadCfg.Aggressiveness)
	if err != nil {
		return vadComponents{}, fmt.Errorf("создать WebRTC detector: %w", err)
	}

	segmenter, err := vad.NewSegmenter(vad.Settings{
		Format:       format,
		PreRoll:      time.Duration(vadCfg.PreRoll),
		MinSpeech:    time.Duration(vadCfg.MinSpeech),
		EndSilence:   time.Duration(vadCfg.EndSilence),
		MaxUtterance: time.Duration(vadCfg.MaxUtterance),
	})
	if err != nil {
		if closeErr := detector.Close(); closeErr != nil {
			return vadComponents{}, fmt.Errorf("создать segmenter: %w (закрыть WebRTC detector: %v)", err, closeErr)
		}
		return vadComponents{}, fmt.Errorf("создать segmenter: %w", err)
	}

	return vadComponents{detector: detector, segmenter: segmenter}, nil
}

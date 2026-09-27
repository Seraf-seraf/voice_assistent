package vad

import (
	"errors"
	"fmt"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/audio"
)

type Settings struct {
	Format       audio.Format
	PreRoll      time.Duration
	MinSpeech    time.Duration
	EndSilence   time.Duration
	MaxUtterance time.Duration
}

type Event interface {
	isEvent()
}

type SpeechStarted struct {
	At time.Time
}

func (SpeechStarted) isEvent() {}

type SpeechEnded struct {
	Utterance audio.Utterance
}

func (SpeechEnded) isEvent() {}

type Segmenter struct {
	settings Settings

	preRollLimit       int
	minSpeechFrames    int
	endSilenceFrames   int
	maxUtteranceFrames int

	preRoll               []audio.Frame
	candidate             []audio.Frame
	candidateSpeechFrames int
	candidateStartedAt    time.Time
	recording             []audio.Frame
	recordingStartedAt    time.Time
	lastSpeechEnd         time.Time
	silenceFrames         int
	nextUtteranceID       uint64
}

func NewSegmenter(settings Settings) (*Segmenter, error) {
	if err := validateSettings(settings); err != nil {
		return nil, err
	}
	return &Segmenter{
		settings:           settings,
		preRollLimit:       framesFor(settings.PreRoll, settings.Format.FrameDuration),
		minSpeechFrames:    framesFor(settings.MinSpeech, settings.Format.FrameDuration),
		endSilenceFrames:   framesFor(settings.EndSilence, settings.Format.FrameDuration),
		maxUtteranceFrames: framesFor(settings.MaxUtterance, settings.Format.FrameDuration),
	}, nil
}

func (s *Segmenter) Process(frame audio.Frame, activity Activity) ([]Event, error) {
	if err := frame.Validate(s.settings.Format); err != nil {
		return nil, fmt.Errorf("проверить аудиокадр: %w", err)
	}
	if activity != Silence && activity != Speech {
		return nil, fmt.Errorf("неизвестная активность %d", activity)
	}

	if len(s.recording) > 0 {
		return s.processRecording(frame, activity), nil
	}
	return s.processWaiting(frame, activity), nil
}

func (s *Segmenter) Flush() []Event {
	if len(s.recording) == 0 {
		s.resetCandidate()
		return nil
	}
	return []Event{s.finishUtterance()}
}

func (s *Segmenter) Reset() {
	s.preRoll = nil
	s.resetCandidate()
	s.resetRecording()
}

func (s *Segmenter) processWaiting(frame audio.Frame, activity Activity) []Event {
	if activity == Silence {
		if len(s.candidate) > 0 {
			for _, candidateFrame := range s.candidate {
				s.pushPreRoll(candidateFrame)
			}
			s.resetCandidate()
		}
		s.pushPreRoll(frame)
		return nil
	}

	if len(s.candidate) == 0 {
		s.candidateStartedAt = frame.CapturedAt
	}
	s.candidate = append(s.candidate, frame)
	s.candidateSpeechFrames++
	if s.candidateSpeechFrames < s.minSpeechFrames {
		return nil
	}

	s.recording = make([]audio.Frame, 0, s.maxUtteranceFrames)
	s.recording = append(s.recording, s.preRoll...)
	s.recording = append(s.recording, s.candidate...)
	s.recordingStartedAt = s.candidateStartedAt
	s.lastSpeechEnd = frame.CapturedAt.Add(s.settings.Format.FrameDuration)
	s.preRoll = nil
	s.resetCandidate()
	return []Event{SpeechStarted{At: s.recordingStartedAt}}
}

func (s *Segmenter) processRecording(frame audio.Frame, activity Activity) []Event {
	s.recording = append(s.recording, frame)
	if activity == Speech {
		s.silenceFrames = 0
		s.lastSpeechEnd = frame.CapturedAt.Add(s.settings.Format.FrameDuration)
	} else {
		s.silenceFrames++
	}

	if s.silenceFrames >= s.endSilenceFrames || len(s.recording) >= s.maxUtteranceFrames {
		return []Event{s.finishUtterance()}
	}
	return nil
}

func (s *Segmenter) finishUtterance() SpeechEnded {
	frames := s.recording
	samples := make([]int16, 0, len(frames)*s.settings.Format.SamplesPerFrame())
	for _, frame := range frames {
		samples = append(samples, frame.Samples...)
	}

	s.nextUtteranceID++
	event := SpeechEnded{Utterance: audio.Utterance{
		ID:         s.nextUtteranceID,
		Samples:    samples,
		Format:     s.settings.Format,
		StartedAt:  s.recordingStartedAt,
		EndedAt:    s.lastSpeechEnd,
		CapturedAt: frames[0].CapturedAt,
	}}

	s.preRoll = nil
	start := len(frames) - s.preRollLimit
	if start < 0 {
		start = 0
	}
	for _, frame := range frames[start:] {
		s.pushPreRoll(frame)
	}
	s.resetRecording()
	return event
}

func (s *Segmenter) pushPreRoll(frame audio.Frame) {
	if s.preRollLimit == 0 {
		return
	}
	if len(s.preRoll) == s.preRollLimit {
		copy(s.preRoll, s.preRoll[1:])
		s.preRoll = s.preRoll[:len(s.preRoll)-1]
	}
	s.preRoll = append(s.preRoll, frame)
}

func (s *Segmenter) resetCandidate() {
	s.candidate = nil
	s.candidateSpeechFrames = 0
	s.candidateStartedAt = time.Time{}
}

func (s *Segmenter) resetRecording() {
	s.recording = nil
	s.recordingStartedAt = time.Time{}
	s.lastSpeechEnd = time.Time{}
	s.silenceFrames = 0
}

func validateSettings(settings Settings) error {
	if err := settings.Format.Validate(); err != nil {
		return fmt.Errorf("формат аудио: %w", err)
	}
	frameDuration := settings.Format.FrameDuration
	if frameDuration != 10*time.Millisecond && frameDuration != 20*time.Millisecond && frameDuration != 30*time.Millisecond {
		return errors.New("VAD поддерживает длительность кадра 10, 20 или 30 мс")
	}
	if settings.PreRoll < 0 || settings.MinSpeech <= 0 || settings.EndSilence <= 0 || settings.MaxUtterance <= 0 {
		return errors.New("интервалы VAD некорректны")
	}
	if settings.MaxUtterance <= settings.MinSpeech {
		return errors.New("максимальная длина реплики должна превышать минимальную длительность речи")
	}
	return nil
}

func framesFor(value, frameDuration time.Duration) int {
	if value == 0 {
		return 0
	}
	return int((value + frameDuration - 1) / frameDuration)
}

package assistant

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Seraf-seraf/voice_assistent/internal/service/audio/output"
	"github.com/Seraf-seraf/voice_assistent/internal/service/diagnostics"
	"github.com/Seraf-seraf/voice_assistent/internal/service/tts"
)

const MaxSpeechResponseRunes = 8192

var (
	ErrSpeechTextLimit    = errors.New("превышен лимит текста речевого ответа")
	ErrSpeechState        = errors.New("некорректное состояние речевого ответа")
	ErrSpeechTextMismatch = errors.New("полный текст ответа не совпадает с принятыми дельтами")
	ErrSpeechOptions      = errors.New("некорректные параметры речевого ответа")
)

type SpeechOptions struct {
	Timeout time.Duration
}

func (o SpeechOptions) Validate() error {
	if o.Timeout <= 0 {
		return fmt.Errorf("%w: время ожидания должно быть положительным", ErrSpeechOptions)
	}
	return nil
}

type SpokenResponseSink struct {
	textSink    ResponseSink
	synthesizer tts.Synthesizer
	player      output.Player
	options     SpeechOptions
	active      *speechSession
}

type speechSession struct {
	utteranceID           uint64
	turnID                uint64
	ctx                   context.Context
	cancel                context.CancelFunc
	jobs                  chan string
	done                  chan struct{}
	workerResult          error
	segmenter             speechSegmenter
	accepted              strings.Builder
	playedText            strings.Builder
	runeCount             int
	inputClosed           bool
	completing            bool
	workerFailureReturned bool
	reportedContextError  error
}

var _ ResponseSink = (*SpokenResponseSink)(nil)

func NewSpokenResponseSink(textSink ResponseSink, synthesizer tts.Synthesizer, player output.Player, options SpeechOptions) (*SpokenResponseSink, error) {
	if textSink == nil || isNilDependency(textSink) || synthesizer == nil || isNilDependency(synthesizer) || player == nil || isNilDependency(player) {
		return nil, errors.New("зависимости речевого приёмника ответа обязательны")
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}
	return &SpokenResponseSink{textSink: textSink, synthesizer: synthesizer, player: player, options: options}, nil
}

func (s *SpokenResponseSink) Push(ctx context.Context, delta ResponseDelta) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	session := s.active
	if session != nil && (session.utteranceID != delta.UtteranceID || session.turnID != delta.TurnID || session.completing) {
		return ErrSpeechState
	}
	if !validSpeechDelta(delta.Text) {
		return fmt.Errorf("%w: дельта должна быть непустым UTF-8 текстом без NUL", ErrSpeechState)
	}
	deltaRunes := utf8.RuneCountInString(delta.Text)
	if session != nil {
		if deltaRunes > MaxSpeechResponseRunes-session.runeCount {
			return ErrSpeechTextLimit
		}
		if err := s.checkWorker(session); err != nil {
			return err
		}
	} else {
		if deltaRunes > MaxSpeechResponseRunes {
			return ErrSpeechTextLimit
		}
		session = s.startSession(ctx, delta)
		s.active = session
	}
	if err := s.textSink.Push(ctx, delta); err != nil {
		return fmt.Errorf("передать текстовому приёмнику ответа: %w", err)
	}
	session.accepted.WriteString(delta.Text)
	session.runeCount += deltaRunes
	segments := session.segmenter.Push(delta.Text)
	for _, segment := range segments {
		diagnostics.Emit(session.ctx, diagnostics.Event{Phase: "phrase_ready", Runes: utf8.RuneCountInString(segment)})
	}
	if err := enqueueSpeechSegments(session, segments); err != nil {
		return err
	}
	return s.checkWorker(session)
}

func (s *SpokenResponseSink) Complete(ctx context.Context, response Response) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	session := s.active
	if session == nil || session.completing || session.utteranceID != response.UtteranceID || session.turnID != response.TurnID {
		return ErrSpeechState
	}
	if response.Text != session.accepted.String() {
		return ErrSpeechTextMismatch
	}
	if err := s.checkWorker(session); err != nil {
		return err
	}
	session.completing = true
	if tail := session.segmenter.Finish(); tail != "" {
		diagnostics.Emit(session.ctx, diagnostics.Event{Phase: "phrase_ready", Runes: utf8.RuneCountInString(tail)})
		if err := enqueueSpeechSegments(session, []string{tail}); err != nil {
			return err
		}
	}
	closeSpeechInput(session)
	if err := s.awaitSpeechWorker(ctx, session); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.textSink.Complete(ctx, response); err != nil {
		return fmt.Errorf("завершить работу текстового приёмника ответа: %w", err)
	}
	session.cancel()
	s.active = nil
	return nil
}

func (s *SpokenResponseSink) Abort(ctx context.Context, abort ResponseAbort) (ResponseAbortResult, error) {
	session := s.active
	initialResult := ResponseAbortResult{UtteranceID: abort.UtteranceID, TurnID: abort.TurnID}
	if session == nil {
		return initialResult, nil
	}
	if session.utteranceID != abort.UtteranceID || session.turnID != abort.TurnID {
		return initialResult, ErrSpeechState
	}
	session.cancel()
	closeSpeechInput(session)
	<-session.done
	var result error
	if session.workerResult != nil && !session.workerFailureReturned {
		wasAlreadyReported := session.reportedContextError != nil && cancellationOnly(session.workerResult, session.reportedContextError)
		if !wasAlreadyReported && !cancellationOnly(session.workerResult, context.Canceled) {
			result = fmt.Errorf("синтез/воспроизведение речевого ответа: %w", session.workerResult)
		}
	}
	if _, err := s.textSink.Abort(ctx, abort); err != nil {
		result = errors.Join(result, fmt.Errorf("очистить текстовый приёмник ответа: %w", err))
	}
	progress := ResponseAbortResult{UtteranceID: abort.UtteranceID, TurnID: abort.TurnID, PlayedText: session.playedText.String()}
	session.cancel()
	s.active = nil
	return progress, result
}

func (s *SpokenResponseSink) startSession(parent context.Context, delta ResponseDelta) *speechSession {
	ctx, cancel := context.WithTimeout(parent, s.options.Timeout)
	session := &speechSession{
		utteranceID: delta.UtteranceID,
		turnID:      delta.TurnID,
		ctx:         diagnostics.WithIDs(ctx, delta.UtteranceID, delta.TurnID),
		cancel:      cancel,
		jobs:        make(chan string, MaxSpeechResponseRunes),
		done:        make(chan struct{}),
	}
	go s.runSpeechWorker(session)
	return session
}

func (s *SpokenResponseSink) runSpeechWorker(session *speechSession) {
	defer close(session.done)
	for {
		select {
		case <-session.ctx.Done():
			session.workerResult = session.ctx.Err()
			return
		case phrase, open := <-session.jobs:
			if !open {
				return
			}
			if err := session.ctx.Err(); err != nil {
				session.workerResult = err
				return
			}
			text := strings.TrimSpace(phrase)
			if text == "" {
				_, _ = session.playedText.WriteString(phrase)
				continue
			}
			if err := tts.ValidateText(text); err != nil {
				session.workerResult = err
				session.cancel()
				return
			}
			phraseRunes := utf8.RuneCountInString(text)
			synthesisStarted := time.Now()
			diagnostics.Emit(session.ctx, diagnostics.Event{Phase: "tts_started", Runes: phraseRunes})
			pcm, err := s.synthesizer.Synthesize(session.ctx, text)
			if err != nil {
				diagnostics.Emit(session.ctx, diagnostics.Event{Phase: "tts_failed", Duration: time.Since(synthesisStarted), Runes: phraseRunes})
				session.workerResult = joinOperationCancellation(err, session.ctx.Err())
				session.cancel()
				return
			}
			diagnostics.Emit(session.ctx, diagnostics.Event{Phase: "tts_completed", Duration: time.Since(synthesisStarted), Runes: phraseRunes, Frames: len(pcm.Samples), SampleRate: pcm.SampleRate})
			if err := session.ctx.Err(); err != nil {
				session.workerResult = err
				return
			}
			if err := s.player.Play(session.ctx, pcm); err != nil {
				session.workerResult = joinOperationCancellation(err, session.ctx.Err())
				session.cancel()
				return
			}
			_, _ = session.playedText.WriteString(phrase)
		}
	}
}

func (s *SpokenResponseSink) checkWorker(session *speechSession) error {
	select {
	case <-session.done:
		if session.workerResult != nil {
			session.workerFailureReturned = true
			return session.workerResult
		}
	default:
	}
	if err := session.ctx.Err(); err != nil {
		select {
		case <-session.done:
			if session.workerResult != nil {
				session.workerFailureReturned = true
				return session.workerResult
			}
		default:
		}
		session.reportedContextError = err
		return err
	}
	return nil
}

func (s *SpokenResponseSink) awaitSpeechWorker(ctx context.Context, session *speechSession) error {
	select {
	case <-session.done:
	case <-ctx.Done():
		session.cancel()
		<-session.done
		workerErr := session.workerResult
		if workerErr != nil && cancellationOnly(workerErr, context.Canceled) {
			workerErr = nil
		} else if workerErr != nil {
			session.workerFailureReturned = true
		}
		return errors.Join(ctx.Err(), workerErr)
	}
	if session.workerResult != nil {
		session.workerFailureReturned = true
		return session.workerResult
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func enqueueSpeechSegments(session *speechSession, segments []string) error {
	for _, segment := range segments {
		select {
		case session.jobs <- segment:
		default:
			return fmt.Errorf("%w: ограниченная очередь речи переполнена", ErrSpeechState)
		}
	}
	return nil
}

func closeSpeechInput(session *speechSession) {
	if !session.inputClosed {
		close(session.jobs)
		session.inputClosed = true
	}
}

func validSpeechDelta(text string) bool {
	return text != "" && utf8.ValidString(text) && !strings.ContainsRune(text, '\x00')
}

func joinOperationCancellation(operationErr, cancellationErr error) error {
	if cancellationErr == nil {
		return operationErr
	}
	return errors.Join(operationErr, cancellationErr)
}

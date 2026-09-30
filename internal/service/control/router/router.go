package router

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Seraf-seraf/voice_assistent/internal/service/transcript"
)

type Mode uint8

const (
	ModeAlways Mode = iota + 1
	ModePTT
	ModeWake
)

type DecisionKind uint8

const (
	DecisionIgnored DecisionKind = iota
	DecisionQuery
	DecisionCommand
	DecisionAwaitingWake
)

type Command uint8

const (
	CommandStop Command = iota + 1
	CommandPause
	CommandResume
	CommandResetHistory
)

type Decision struct {
	Kind      DecisionKind
	Text      string
	Canonical string
	Command   Command
}

type Options struct {
	Mode        Mode
	WakePhrases []string
	WakeWindow  time.Duration
}

type wakePhrase struct {
	canonical string
	runes     int
}

type Router struct {
	mode        Mode
	wakePhrases []wakePhrase
	wakeWindow  time.Duration
	wakeUntil   time.Time
	paused      bool
}

var commands = map[string]Command{
	"стоп":           CommandStop,
	"пауза":          CommandPause,
	"продолжай":      CommandResume,
	"очисти историю": CommandResetHistory,
}

func New(options Options) (*Router, error) {
	if options.Mode != ModeAlways && options.Mode != ModePTT && options.Mode != ModeWake {
		return nil, fmt.Errorf("неизвестный режим активации %d", options.Mode)
	}
	router := &Router{mode: options.Mode, wakeWindow: options.WakeWindow}
	if options.Mode != ModeWake {
		return router, nil
	}
	if options.WakeWindow <= 0 {
		return nil, errors.New("окно активации должно быть положительным")
	}
	if len(options.WakePhrases) == 0 {
		return nil, errors.New("нужна хотя бы одна фраза активации")
	}
	seen := make(map[string]struct{}, len(options.WakePhrases))
	for _, phrase := range options.WakePhrases {
		canonical := transcript.Canonicalize(phrase)
		if canonical == "" {
			return nil, errors.New("фраза активации не может быть пустой")
		}
		if _, exists := seen[canonical]; exists {
			continue
		}
		seen[canonical] = struct{}{}
		router.wakePhrases = append(router.wakePhrases, wakePhrase{
			canonical: canonical, runes: utf8.RuneCountInString(canonical),
		})
	}
	sort.Slice(router.wakePhrases, func(i, j int) bool {
		return router.wakePhrases[i].runes > router.wakePhrases[j].runes
	})
	return router, nil
}

func ParseMode(value string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "always":
		return ModeAlways, nil
	case "ptt":
		return ModePTT, nil
	case "wake":
		return ModeWake, nil
	default:
		return 0, fmt.Errorf("неизвестный режим активации %q", value)
	}
}

func (r *Router) Route(input transcript.Result, now time.Time) Decision {
	if now.IsZero() {
		now = time.Now()
	}
	if !r.wakeUntil.IsZero() && now.After(r.wakeUntil) {
		r.wakeUntil = time.Time{}
	}
	if command, exists := commands[input.Canonical]; exists {
		return r.applyCommand(command)
	}
	if r.paused {
		return Decision{Kind: DecisionIgnored}
	}
	if r.mode != ModeWake {
		return queryDecision(input)
	}

	if remainder, matched := r.stripWakePhrase(input); matched {
		if remainder.Canonical == "" {
			r.wakeUntil = now.Add(r.wakeWindow)
			return Decision{Kind: DecisionAwaitingWake}
		}
		r.wakeUntil = time.Time{}
		if command, exists := commands[remainder.Canonical]; exists {
			return r.applyCommand(command)
		}
		return queryDecision(remainder)
	}
	if !r.wakeUntil.IsZero() {
		r.wakeUntil = time.Time{}
		return queryDecision(input)
	}
	return Decision{Kind: DecisionIgnored}
}

func (r *Router) applyCommand(command Command) Decision {
	switch command {
	case CommandPause:
		r.paused = true
		r.wakeUntil = time.Time{}
	case CommandResume:
		r.paused = false
	case CommandStop, CommandResetHistory:
	}
	return Decision{Kind: DecisionCommand, Command: command}
}

func (r *Router) stripWakePhrase(input transcript.Result) (transcript.Result, bool) {
	for _, phrase := range r.wakePhrases {
		if !hasPhrasePrefix(input.Canonical, phrase.canonical) {
			continue
		}
		text := strings.TrimLeftFunc(input.Text, isBoundary)
		runes := []rune(text)
		if len(runes) < phrase.runes {
			return transcript.Result{}, true
		}
		text = strings.TrimLeftFunc(string(runes[phrase.runes:]), isBoundary)
		return transcript.Result{Text: text, Canonical: transcript.Canonicalize(text)}, true
	}
	return transcript.Result{}, false
}

func hasPhrasePrefix(value, phrase string) bool {
	if !strings.HasPrefix(value, phrase) {
		return false
	}
	remainder := value[len(phrase):]
	if remainder == "" {
		return true
	}
	first, _ := utf8.DecodeRuneInString(remainder)
	return isBoundary(first)
}

func isBoundary(r rune) bool {
	return unicode.IsSpace(r) || unicode.IsPunct(r)
}

func queryDecision(input transcript.Result) Decision {
	return Decision{Kind: DecisionQuery, Text: input.Text, Canonical: input.Canonical}
}

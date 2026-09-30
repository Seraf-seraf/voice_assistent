package router

import (
	"testing"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/service/transcript"
)

func TestRouterPassesQueriesInAlwaysAndPTTModes(t *testing.T) {
	for _, mode := range []Mode{ModeAlways, ModePTT} {
		router := newTestRouter(t, Options{Mode: mode})
		decision := router.Route(input("Привет"), time.Now())
		if decision.Kind != DecisionQuery || decision.Text != "Привет" {
			t.Fatalf("mode %d decision = %+v", mode, decision)
		}
	}
}

func TestRouterProcessesCommandsWithoutWakeWord(t *testing.T) {
	router := newTestRouter(t, wakeOptions())
	tests := []struct {
		text    string
		command Command
	}{
		{"стоп!", CommandStop},
		{"пауза", CommandPause},
		{"продолжай", CommandResume},
		{"очисти историю", CommandResetHistory},
	}
	for _, test := range tests {
		decision := router.Route(input(test.text), time.Now())
		if decision.Kind != DecisionCommand || decision.Command != test.command {
			t.Fatalf("Route(%q) = %+v", test.text, decision)
		}
	}
}

func TestRouterPauseBlocksQueriesUntilResume(t *testing.T) {
	router := newTestRouter(t, Options{Mode: ModeAlways})
	router.Route(input("пауза"), time.Now())
	if decision := router.Route(input("обычный вопрос"), time.Now()); decision.Kind != DecisionIgnored {
		t.Fatalf("paused decision = %+v", decision)
	}
	router.Route(input("продолжай"), time.Now())
	if decision := router.Route(input("обычный вопрос"), time.Now()); decision.Kind != DecisionQuery {
		t.Fatalf("resumed decision = %+v", decision)
	}
}

func TestRouterExtractsQueryAfterWakePhrase(t *testing.T) {
	router := newTestRouter(t, wakeOptions())
	decision := router.Route(input("Ассистент, Какая погода?"), time.Now())
	if decision.Kind != DecisionQuery || decision.Text != "Какая погода?" || decision.Canonical != "какая погода" {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestRouterAcceptsOnePhraseInsideWakeWindow(t *testing.T) {
	router := newTestRouter(t, wakeOptions())
	now := time.Unix(100, 0)
	if decision := router.Route(input("ассистент"), now); decision.Kind != DecisionAwaitingWake {
		t.Fatalf("activation decision = %+v", decision)
	}
	if decision := router.Route(input("расскажи новости"), now.Add(9*time.Second)); decision.Kind != DecisionQuery {
		t.Fatalf("window decision = %+v", decision)
	}
	if decision := router.Route(input("ещё вопрос"), now.Add(9*time.Second)); decision.Kind != DecisionIgnored {
		t.Fatalf("second decision = %+v", decision)
	}
}

func TestRouterExpiresWakeWindow(t *testing.T) {
	router := newTestRouter(t, wakeOptions())
	now := time.Unix(100, 0)
	router.Route(input("ассистент"), now)
	if decision := router.Route(input("слишком поздно"), now.Add(11*time.Second)); decision.Kind != DecisionIgnored {
		t.Fatalf("expired decision = %+v", decision)
	}
}

func TestRouterDoesNotMatchWakePhraseInsideWord(t *testing.T) {
	router := newTestRouter(t, wakeOptions())
	if decision := router.Route(input("ассистентов много"), time.Now()); decision.Kind != DecisionIgnored {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestRouterProcessesCommandAfterWakePhrase(t *testing.T) {
	router := newTestRouter(t, wakeOptions())
	decision := router.Route(input("ассистент, очисти историю"), time.Now())
	if decision.Kind != DecisionCommand || decision.Command != CommandResetHistory {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestRouterValidatesOptions(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("New() accepted unknown mode")
	}
	if _, err := New(Options{Mode: ModeWake, WakeWindow: time.Second}); err == nil {
		t.Fatal("New() accepted empty wake phrases")
	}
	if _, err := New(Options{Mode: ModeWake, WakePhrases: []string{"ассистент"}}); err == nil {
		t.Fatal("New() accepted zero wake window")
	}
	if _, err := ParseMode("unknown"); err == nil {
		t.Fatal("ParseMode() accepted unknown mode")
	}
}

func wakeOptions() Options {
	return Options{Mode: ModeWake, WakePhrases: []string{"ассистент"}, WakeWindow: 10 * time.Second}
}

func newTestRouter(t *testing.T, options Options) *Router {
	t.Helper()
	router, err := New(options)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	return router
}

func input(text string) transcript.Result {
	return transcript.Result{Text: text, Canonical: transcript.Canonicalize(text)}
}

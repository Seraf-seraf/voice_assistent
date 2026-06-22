package dialogue

import (
	"errors"
	"reflect"
	"testing"
)

func TestManagerStoresOnlySpokenTextOnInterruption(t *testing.T) {
	manager := newTestManager(t, 20)
	turnID := beginTurn(t, manager, "Расскажи историю")
	if err := manager.RecordSpoken(turnID, "Жил-был кот."); err != nil {
		t.Fatalf("RecordSpoken() error: %v", err)
	}
	if err := manager.RecordSpoken(turnID, "Он любил спать."); err != nil {
		t.Fatalf("RecordSpoken() error: %v", err)
	}
	if err := manager.InterruptTurn(turnID); err != nil {
		t.Fatalf("InterruptTurn() error: %v", err)
	}

	want := []Message{
		{Role: RoleUser, Content: "Расскажи историю"},
		{Role: RoleAssistant, Content: "Жил-был кот. Он любил спать."},
	}
	if got := manager.Snapshot().Messages; !reflect.DeepEqual(got, want) {
		t.Fatalf("Messages = %#v, want %#v", got, want)
	}
}

func TestManagerStoresFullCompletedResponse(t *testing.T) {
	manager := newTestManager(t, 20)
	turnID := beginTurn(t, manager, "Вопрос")
	if err := manager.RecordSpoken(turnID, "Начало ответа."); err != nil {
		t.Fatalf("RecordSpoken() error: %v", err)
	}
	if err := manager.CompleteTurn(turnID, "Начало ответа. Полный ответ."); err != nil {
		t.Fatalf("CompleteTurn() error: %v", err)
	}

	messages := manager.Snapshot().Messages
	if got := messages[len(messages)-1].Content; got != "Начало ответа. Полный ответ." {
		t.Fatalf("assistant content = %q", got)
	}
}

func TestManagerAbortLeavesUserMessage(t *testing.T) {
	manager := newTestManager(t, 20)
	turnID := beginTurn(t, manager, "Вопрос без ответа")
	if err := manager.AbortTurn(turnID); err != nil {
		t.Fatalf("AbortTurn() error: %v", err)
	}

	want := []Message{{Role: RoleUser, Content: "Вопрос без ответа"}}
	if got := manager.Snapshot().Messages; !reflect.DeepEqual(got, want) {
		t.Fatalf("Messages = %#v, want %#v", got, want)
	}
}

func TestManagerTrimsOldestCompleteTurns(t *testing.T) {
	manager := newTestManager(t, 4)
	for _, text := range []string{"первый", "второй", "третий"} {
		turnID := beginTurn(t, manager, text)
		if err := manager.CompleteTurn(turnID, "ответ "+text); err != nil {
			t.Fatalf("CompleteTurn() error: %v", err)
		}
	}

	want := []Message{
		{Role: RoleUser, Content: "второй"}, {Role: RoleAssistant, Content: "ответ второй"},
		{Role: RoleUser, Content: "третий"}, {Role: RoleAssistant, Content: "ответ третий"},
	}
	if got := manager.Snapshot().Messages; !reflect.DeepEqual(got, want) {
		t.Fatalf("Messages = %#v, want %#v", got, want)
	}
}

func TestManagerValidatesTurnLifecycle(t *testing.T) {
	manager := newTestManager(t, 20)
	turnID := beginTurn(t, manager, "первый")
	if _, err := manager.BeginTurn("второй"); !errors.Is(err, ErrTurnActive) {
		t.Fatalf("BeginTurn() error = %v, want ErrTurnActive", err)
	}
	if err := manager.RecordSpoken(turnID+1, "ответ"); !errors.Is(err, ErrTurnMismatch) {
		t.Fatalf("RecordSpoken() error = %v, want ErrTurnMismatch", err)
	}
	if err := manager.AbortTurn(turnID); err != nil {
		t.Fatalf("AbortTurn() error: %v", err)
	}
	if err := manager.InterruptTurn(turnID); !errors.Is(err, ErrNoActiveTurn) {
		t.Fatalf("InterruptTurn() error = %v, want ErrNoActiveTurn", err)
	}
}

func TestManagerResetAndSnapshotIsolation(t *testing.T) {
	manager := newTestManager(t, 20)
	turnID := beginTurn(t, manager, "текст")
	if err := manager.AbortTurn(turnID); err != nil {
		t.Fatalf("AbortTurn() error: %v", err)
	}
	snapshot := manager.Snapshot()
	snapshot.Messages[0].Content = "изменено"
	if manager.Snapshot().Messages[0].Content != "текст" {
		t.Fatal("Snapshot() exposes internal message storage")
	}
	manager.Reset()
	if len(manager.Snapshot().Messages) != 0 {
		t.Fatal("Reset() did not clear history")
	}
}

func TestNewManagerValidatesOptions(t *testing.T) {
	tests := []Options{
		{SystemPrompt: "", ResponsePolicy: "policy", MaxHistoryMessages: 2},
		{SystemPrompt: "system", ResponsePolicy: "", MaxHistoryMessages: 2},
		{SystemPrompt: "system", ResponsePolicy: "policy", MaxHistoryMessages: 1},
	}
	for _, options := range tests {
		if _, err := New(options); err == nil {
			t.Fatalf("New(%+v) succeeded, want error", options)
		}
	}
}

func newTestManager(t *testing.T, maxMessages int) *Manager {
	t.Helper()
	manager, err := New(Options{
		SystemPrompt: "system", ResponsePolicy: "policy", MaxHistoryMessages: maxMessages,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	return manager
}

func beginTurn(t *testing.T, manager *Manager, text string) uint64 {
	t.Helper()
	turnID, err := manager.BeginTurn(text)
	if err != nil {
		t.Fatalf("BeginTurn() error: %v", err)
	}
	return turnID
}

package dialogue

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

var (
	ErrTurnActive   = errors.New("предыдущий dialogue turn ещё активен")
	ErrNoActiveTurn = errors.New("активный dialogue turn отсутствует")
	ErrTurnMismatch = errors.New("dialogue turn ID не совпадает")
	ErrEmptyMessage = errors.New("dialogue message не может быть пустым")
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

type Message struct {
	Role    Role
	Content string
}

type Snapshot struct {
	SystemPrompt   string
	ResponsePolicy string
	Messages       []Message
}

type Options struct {
	SystemPrompt       string
	ResponsePolicy     string
	MaxHistoryMessages int
}

type Manager struct {
	options Options
	history []Message

	nextTurnID uint64
	activeTurn uint64
	spoken     []string
}

func New(options Options) (*Manager, error) {
	options.SystemPrompt = strings.TrimSpace(options.SystemPrompt)
	options.ResponsePolicy = strings.TrimSpace(options.ResponsePolicy)
	if options.SystemPrompt == "" {
		return nil, errors.New("system prompt обязателен")
	}
	if options.ResponsePolicy == "" {
		return nil, errors.New("response policy обязательна")
	}
	if options.MaxHistoryMessages < 2 {
		return nil, errors.New("max history messages должен быть не меньше 2")
	}
	return &Manager{options: options}, nil
}

func (m *Manager) BeginTurn(userText string) (uint64, error) {
	if m.activeTurn != 0 {
		return 0, ErrTurnActive
	}
	userText = strings.TrimSpace(userText)
	if userText == "" {
		return 0, ErrEmptyMessage
	}
	m.nextTurnID++
	m.activeTurn = m.nextTurnID
	m.spoken = nil
	m.history = append(m.history, Message{Role: RoleUser, Content: userText})
	m.trimHistory()
	return m.activeTurn, nil
}

func (m *Manager) RecordSpoken(turnID uint64, text string) error {
	if err := m.validateActiveTurn(turnID); err != nil {
		return err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return ErrEmptyMessage
	}
	m.spoken = append(m.spoken, text)
	return nil
}

func (m *Manager) CompleteTurn(turnID uint64, fullResponse string) error {
	if err := m.validateActiveTurn(turnID); err != nil {
		return err
	}
	fullResponse = strings.TrimSpace(fullResponse)
	if fullResponse == "" {
		return ErrEmptyMessage
	}
	m.appendAssistant(fullResponse)
	m.finishTurn()
	return nil
}

func (m *Manager) InterruptTurn(turnID uint64) error {
	if err := m.validateActiveTurn(turnID); err != nil {
		return err
	}
	if len(m.spoken) > 0 {
		m.appendAssistant(strings.Join(m.spoken, " "))
	}
	m.finishTurn()
	return nil
}

func (m *Manager) AbortTurn(turnID uint64) error {
	if err := m.validateActiveTurn(turnID); err != nil {
		return err
	}
	m.finishTurn()
	return nil
}

func (m *Manager) Reset() {
	m.history = nil
	m.activeTurn = 0
	m.spoken = nil
}

func (m *Manager) Snapshot() Snapshot {
	messages := make([]Message, len(m.history))
	copy(messages, m.history)
	return Snapshot{
		SystemPrompt: m.options.SystemPrompt, ResponsePolicy: m.options.ResponsePolicy, Messages: messages,
	}
}

func (m *Manager) validateActiveTurn(turnID uint64) error {
	if m.activeTurn == 0 {
		return ErrNoActiveTurn
	}
	if turnID != m.activeTurn {
		return fmt.Errorf("%w: активный=%d, получен=%d", ErrTurnMismatch, m.activeTurn, turnID)
	}
	return nil
}

func (m *Manager) appendAssistant(text string) {
	m.history = append(m.history, Message{Role: RoleAssistant, Content: text})
	m.trimHistory()
}

func (m *Manager) finishTurn() {
	m.activeTurn = 0
	m.spoken = nil
}

func (m *Manager) trimHistory() {
	if overflow := len(m.history) - m.options.MaxHistoryMessages; overflow > 0 {
		m.history = slices.Delete(m.history, 0, overflow)
	}
	for len(m.history) > 0 && m.history[0].Role == RoleAssistant {
		m.history = slices.Delete(m.history, 0, 1)
	}
}

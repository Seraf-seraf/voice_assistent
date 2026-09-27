package llamacpp

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Seraf-seraf/voice_assistent/internal/dialogue"
	"github.com/Seraf-seraf/voice_assistent/internal/llm"
	"github.com/hybridgroup/yzma/pkg/message"
	"github.com/hybridgroup/yzma/pkg/template"
)

func renderPrompt(templateText string, snapshot dialogue.Snapshot) (string, error) {
	if templateText == "" || snapshot.SystemPrompt == "" || snapshot.ResponsePolicy == "" || len(snapshot.Messages) == 0 {
		return "", fmt.Errorf("%w: template, system prompt, policy и messages обязательны", ErrInvalidRequest)
	}
	if err := validatePromptText(templateText); err != nil {
		return "", fmt.Errorf("%w: template: %v", ErrInvalidRequest, err)
	}
	if err := validatePromptText(snapshot.SystemPrompt); err != nil {
		return "", fmt.Errorf("%w: system prompt: %v", ErrInvalidRequest, err)
	}
	if err := validatePromptText(snapshot.ResponsePolicy); err != nil {
		return "", fmt.Errorf("%w: response policy: %v", ErrInvalidRequest, err)
	}
	messages := make([]message.Message, 0, len(snapshot.Messages)+1)
	messages = append(messages, message.Chat{Role: string(dialogue.RoleSystem), Content: snapshot.SystemPrompt + "\n\n" + snapshot.ResponsePolicy})
	for i, item := range snapshot.Messages {
		if item.Role != dialogue.RoleSystem && item.Role != dialogue.RoleUser && item.Role != dialogue.RoleAssistant {
			return "", fmt.Errorf("%w: неизвестная роль в messages[%d]", ErrInvalidRequest, i)
		}
		if err := validatePromptText(item.Content); err != nil {
			return "", fmt.Errorf("%w: messages[%d]: %v", ErrInvalidRequest, i, err)
		}
		messages = append(messages, message.Chat{Role: string(item.Role), Content: item.Content})
	}
	if snapshot.Messages[len(snapshot.Messages)-1].Role != dialogue.RoleUser {
		return "", fmt.Errorf("%w: последнее сообщение должно быть user", ErrInvalidRequest)
	}
	prompt, err := template.ApplyWithOptions(templateText, messages, true, template.Options{EnableThinking: false})
	if err != nil {
		return "", errors.Join(
			fmt.Errorf("%w: отрендерить chat template", ErrInvalidRequest),
			fmt.Errorf("chat template: %w", err),
		)
	}
	if prompt == "" || strings.IndexByte(prompt, 0) >= 0 || !utf8.ValidString(prompt) {
		return "", fmt.Errorf("%w: renderer вернул некорректный prompt", ErrInvalidRequest)
	}
	return prompt, nil
}

func validatePromptText(value string) error {
	if !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 {
		return fmt.Errorf("текст должен быть UTF-8 и не содержать NUL")
	}
	return nil
}

func containsNUL(value string) bool { return strings.IndexByte(value, 0) >= 0 }

var _ llm.Generator = (*Generator)(nil)

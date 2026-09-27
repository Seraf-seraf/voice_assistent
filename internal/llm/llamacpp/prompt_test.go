package llamacpp

import (
	"strings"
	"testing"

	"github.com/Seraf-seraf/voice_assistent/internal/dialogue"
)

func TestRenderPromptPreservesMessageOrderAndDisablesThinking(t *testing.T) {
	const templateText = `{% for message in messages %}[{{ message.role }}:{{ message.content }}]{% endfor %}{% if enable_thinking %}thinking{% endif %}{% if add_generation_prompt %}[assistant]{% endif %}`
	snapshot := dialogue.Snapshot{
		SystemPrompt: "system instruction", ResponsePolicy: "policy instruction",
		Messages: []dialogue.Message{
			{Role: dialogue.RoleUser, Content: "first"},
			{Role: dialogue.RoleAssistant, Content: "answer"},
			{Role: dialogue.RoleUser, Content: "current"},
		},
	}
	prompt, err := renderPrompt(templateText, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	want := "[system:system instruction\n\npolicy instruction][user:first][assistant:answer][user:current][assistant]"
	if prompt != want {
		t.Fatalf("prompt=%q want=%q", prompt, want)
	}
	if strings.Count(prompt, "current") != 1 || strings.Contains(prompt, "thinking") {
		t.Fatalf("prompt duplicated current user or enabled thinking: %q", prompt)
	}
}

func TestRenderPromptRejectsInvalidSnapshot(t *testing.T) {
	for _, snapshot := range []dialogue.Snapshot{
		{},
		{SystemPrompt: "system", ResponsePolicy: "policy", Messages: []dialogue.Message{{Role: "tool", Content: "x"}}},
		{SystemPrompt: "system", ResponsePolicy: "policy", Messages: []dialogue.Message{{Role: dialogue.RoleAssistant, Content: "x"}}},
		{SystemPrompt: "system\x00", ResponsePolicy: "policy", Messages: []dialogue.Message{{Role: dialogue.RoleUser, Content: "x"}}},
	} {
		if _, err := renderPrompt("{{ messages }}", snapshot); err == nil {
			t.Fatalf("invalid snapshot accepted: %+v", snapshot)
		}
	}
}

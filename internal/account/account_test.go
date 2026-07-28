package account

import (
	"testing"

	"github.com/circlesac/nosnitch-cli/internal/chatgpt"
	"github.com/circlesac/nosnitch-cli/internal/claude"
)

func TestClaudeAccountRisk(t *testing.T) {
	off := false
	tests := []struct {
		name    string
		account Account
		want    bool
	}{
		{"clean", Account{Provider: "anthropic", ModelImprovement: &off, SharedChatsChecked: true}, false},
		{"shared chat", Account{
			Provider:            "anthropic",
			ModelImprovement:    &off,
			SharedConversations: []claude.SharedConversation{{URL: "https://claude.ai/share/test"}},
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.account.Risk(); got != tt.want {
				t.Fatalf("Risk() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCodexTrainingUnknownMakesReportIndeterminate(t *testing.T) {
	off := false
	tests := []struct {
		name    string
		account *Account
		want    bool
	}{
		{
			name: "Codex CLI without ChatGPT training setting",
			account: &Account{
				Provider: "openai",
				Sources:  []string{"Codex CLI"},
			},
			want: true,
		},
		{
			name: "Codex CLI with ChatGPT training setting",
			account: &Account{
				Provider: "openai",
				Sources:  []string{"Codex CLI"},
				Training: map[string]*bool{chatgpt.CodexTrainingFeatureKey: &off},
			},
			want: false,
		},
		{
			name: "browser-only OpenAI account",
			account: &Account{
				Provider: "openai",
				Sources:  []string{"Chrome"},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep := Report{Accounts: []*Account{tt.account}}
			if got := rep.Indeterminate(); got != tt.want {
				t.Fatalf("Indeterminate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAppendSharedUnique(t *testing.T) {
	shared := claude.SharedConversation{URL: "https://claude.ai/share/test"}
	got := appendSharedUnique([]claude.SharedConversation{shared}, []claude.SharedConversation{shared})
	if len(got) != 1 {
		t.Fatalf("len(appendSharedUnique()) = %d", len(got))
	}
}

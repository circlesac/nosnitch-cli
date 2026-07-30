package account

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/circlesac/nosnitch-cli/internal/chatgpt"
	"github.com/circlesac/nosnitch-cli/internal/claude"
	githubprivacy "github.com/circlesac/nosnitch-cli/internal/github"
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

func TestGitHubAccountRiskAndIncomplete(t *testing.T) {
	on, off := true, false
	clean := &Account{
		Provider: "github",
		Login:    "octocat",
		Plan:     "Copilot Pro",
		GitHubCopilot: &githubprivacy.Settings{
			ModelTraining:          &off,
			CloudAgentRepositories: "all",
			PartnerAgents:          map[string]*bool{"claude": &on, "codex": &off},
		},
	}
	if clean.Risk() {
		t.Fatal("broad repository or partner-agent access must not be classified as training exposure")
	}
	if clean.GitHubIncomplete() {
		t.Fatal("complete GitHub result reported incomplete")
	}
	clean.GitHubCopilot.ModelTraining = &on
	if !clean.Risk() {
		t.Fatal("enabled GitHub model training was not classified as exposure")
	}
	clean.GitHubCopilot.ModelTraining = nil
	if !clean.GitHubIncomplete() {
		t.Fatal("missing GitHub model-training setting was not incomplete")
	}
	clean.GitHubCopilot.ModelTraining = &off
	delete(clean.GitHubCopilot.PartnerAgents, "codex")
	if !clean.GitHubIncomplete() {
		t.Fatal("missing Codex partner-agent setting was not incomplete")
	}
}

func TestGitHubBrowserSessionsMergeByLogin(t *testing.T) {
	off := false
	index := newAccountIndex()
	result := githubprivacy.Result{
		OK: true, Login: "octocat", License: "Copilot Pro",
		Settings: githubprivacy.Settings{
			ModelTraining:          &off,
			CloudAgentRepositories: "selected",
			SelectedRepositories:   2,
			PartnerAgents:          map[string]*bool{"claude": &off, "codex": &off},
		},
	}
	mergeGitHub(index.get("github", result.Login), result, "Chrome")
	mergeGitHub(index.get("github", result.Login), githubprivacy.Result{OK: true, Login: "octocat"}, "Safari")

	accounts := index.accounts()
	if len(accounts) != 1 {
		t.Fatalf("merged account count = %d, want 1", len(accounts))
	}
	if strings.Join(accounts[0].Sources, ",") != "Chrome,Safari" {
		t.Fatalf("merged sources = %#v", accounts[0].Sources)
	}
	encoded, _ := json.Marshal(Report{Accounts: accounts})
	for _, field := range []string{`"provider":"github"`, `"login":"octocat"`,
		`"model_training":false`, `"cloud_agent_repositories":"selected"`,
		`"partner_agents"`} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("GitHub JSON missing %s: %s", field, encoded)
		}
	}
}

func TestSkippedSessionMakesReportIndeterminate(t *testing.T) {
	off := false
	report := Report{
		Accounts: []*Account{{
			Provider: "github", Login: "octocat", Plan: "Copilot Pro",
			GitHubCopilot: &githubprivacy.Settings{
				ModelTraining: &off, CloudAgentRepositories: "none",
				PartnerAgents: map[string]*bool{"claude": &off, "codex": &off},
			},
		}},
		Skipped: []string{"Chrome (GitHub): expired session"},
	}
	if !report.Indeterminate() {
		t.Fatal("skipped GitHub session did not make report incomplete")
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

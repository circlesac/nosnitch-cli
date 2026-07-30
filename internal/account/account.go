// Package account aggregates CLI and browser sources into account-centric
// OpenAI Account, Claude Account, and GitHub Account views.
package account

import (
	"errors"
	"sort"

	"github.com/circlesac/nosnitch-cli/internal/chatgpt"
	"github.com/circlesac/nosnitch-cli/internal/claude"
	"github.com/circlesac/nosnitch-cli/internal/codex"
	"github.com/circlesac/nosnitch-cli/internal/cookies"
	githubprivacy "github.com/circlesac/nosnitch-cli/internal/github"
)

type Account struct {
	Provider            string                      `json:"provider"`
	Email               string                      `json:"email,omitempty"`
	Login               string                      `json:"login,omitempty"`
	Plan                string                      `json:"plan,omitempty"`
	Sources             []string                    `json:"sources"`
	APIDataSharing      *bool                       `json:"api_data_sharing,omitempty"`
	Training            map[string]*bool            `json:"training,omitempty"` // feature key -> value
	ModelImprovement    *bool                       `json:"model_improvement,omitempty"`
	SharedConversations []claude.SharedConversation `json:"shared_conversations,omitempty"`
	SharedChatsChecked  bool                        `json:"shared_chats_checked,omitempty"`
	GitHubCopilot       *githubprivacy.Settings     `json:"github_copilot,omitempty"`
}

func (a *Account) Risk() bool {
	if truthy(a.APIDataSharing) {
		return true
	}
	for _, v := range a.Training {
		if truthy(v) {
			return true
		}
	}
	if a.GitHubCopilot != nil && truthy(a.GitHubCopilot.ModelTraining) {
		return true
	}
	return truthy(a.ModelImprovement) || len(a.SharedConversations) > 0
}

func (a *Account) GitHubIncomplete() bool {
	return a.Provider == "github" && (a.Plan == "" || a.GitHubCopilot == nil ||
		a.GitHubCopilot.ModelTraining == nil || a.GitHubCopilot.CloudAgentRepositories == "" ||
		a.GitHubCopilot.PartnerAgents == nil || a.GitHubCopilot.PartnerAgents["claude"] == nil ||
		a.GitHubCopilot.PartnerAgents["codex"] == nil)
}

// CodexTrainingUnknown reports whether a Codex CLI account was found without
// successfully reading its ChatGPT account-level Codex training setting.
func (a *Account) CodexTrainingUnknown() bool {
	return a.Provider == "openai" &&
		contains(a.Sources, "Codex CLI") &&
		a.Training[chatgpt.CodexTrainingFeatureKey] == nil
}

// Blocked is a source we found but couldn't read (e.g. Safari without FDA).
type Blocked struct {
	Source string `json:"source"`
	Reason string `json:"reason"`
}

type Report struct {
	Accounts []*Account `json:"accounts"`
	Blocked  []Blocked  `json:"blocked,omitempty"`
	Skipped  []string   `json:"skipped,omitempty"` // sources present but unreadable/no session
}

func (r Report) Risk() bool {
	for _, a := range r.Accounts {
		if a.Risk() {
			return true
		}
	}
	return false
}

func (r Report) Indeterminate() bool {
	if len(r.Accounts) == 0 {
		return true
	}
	if len(r.Blocked) > 0 || len(r.Skipped) > 0 {
		return true
	}
	for _, a := range r.Accounts {
		if a.CodexTrainingUnknown() {
			return true
		}
		if a.Provider == "anthropic" &&
			(a.ModelImprovement == nil || !a.SharedChatsChecked) {
			return true
		}
		if a.GitHubIncomplete() {
			return true
		}
	}
	return false
}

type accountIndex struct {
	byIdentity map[string]*Account
	order      []string
}

func newAccountIndex() *accountIndex {
	return &accountIndex{byIdentity: map[string]*Account{}}
}

func (i *accountIndex) get(provider, identity string) *Account {
	key := provider + "\x00" + identity
	if existing, ok := i.byIdentity[key]; ok {
		return existing
	}
	created := &Account{Provider: provider}
	if provider == "github" {
		created.Login = identity
	} else {
		created.Email = identity
	}
	i.byIdentity[key] = created
	i.order = append(i.order, key)
	return created
}

func (i *accountIndex) accounts() []*Account {
	sort.Strings(i.order)
	accounts := make([]*Account, 0, len(i.order))
	for _, key := range i.order {
		accounts = append(accounts, i.byIdentity[key])
	}
	return accounts
}

// Gather reads supported CLI and browser sessions and groups them by provider
// plus email, so the same account discovered through multiple sources is merged.
func Gather() Report {
	accounts := newAccountIndex()
	var rep Report

	if cx := codex.Check(); cx.OK {
		a := accounts.get("openai", cx.Email)
		a.Plan = cx.Plan
		a.Sources = append(a.Sources, "Codex CLI")
		v := cx.APIDataSharing
		a.APIDataSharing = &v
	}

	if cc := claude.CheckCode(); cc.OK {
		a := accounts.get("anthropic", cc.Email)
		a.Plan = cc.Plan
		a.Sources = append(a.Sources, "Claude Code")
		a.ModelImprovement = cc.ModelImprovement
		if cc.Reason != "" {
			rep.Skipped = append(rep.Skipped, "Claude Code: "+cc.Reason)
		}
	}

	for _, b := range cookies.Installed() {
		jar, err := b.ChatGPT()
		if errors.Is(err, cookies.ErrNeedFullDiskAccess) {
			rep.Blocked = append(rep.Blocked, Blocked{b.Name, "needs Full Disk Access"})
		} else if err == nil && jar != nil {
			res := chatgpt.CheckWith(jar)
			if !res.OK {
				rep.Skipped = append(rep.Skipped, b.Name+" (OpenAI): "+res.Reason)
			} else {
				a := accounts.get("openai", res.Email)
				a.Sources = appendUnique(a.Sources, b.Name)
				if a.Training == nil {
					a.Training = map[string]*bool{}
				}
				for k, v := range res.Training {
					if v != nil {
						a.Training[k] = v
					}
				}
			}
		}

		claudeJar, err := b.Claude()
		if errors.Is(err, cookies.ErrNeedFullDiskAccess) {
			if !hasBlocked(rep.Blocked, b.Name) {
				rep.Blocked = append(rep.Blocked, Blocked{b.Name, "needs Full Disk Access"})
			}
		} else if err != nil {
			rep.Skipped = append(rep.Skipped, b.Name+" (Claude): "+err.Error())
		} else if claudeJar != nil {
			claudeRes := claude.CheckWeb(claudeJar)
			if !claudeRes.OK {
				rep.Skipped = append(rep.Skipped, b.Name+" (Claude): "+claudeRes.Reason)
			} else {
				a := accounts.get("anthropic", claudeRes.Email)
				a.Sources = appendUnique(a.Sources, b.Name)
				if claudeRes.ModelImprovement != nil {
					a.ModelImprovement = claudeRes.ModelImprovement
				}
				a.SharedConversations = appendSharedUnique(a.SharedConversations, claudeRes.SharedConversations)
				a.SharedChatsChecked = true
			}
		}

		githubJar, err := b.GitHub()
		if errors.Is(err, cookies.ErrNeedFullDiskAccess) {
			if !hasBlocked(rep.Blocked, b.Name) {
				rep.Blocked = append(rep.Blocked, Blocked{b.Name, "needs Full Disk Access"})
			}
		} else if err != nil {
			rep.Skipped = append(rep.Skipped, b.Name+" (GitHub): "+err.Error())
		} else if githubJar != nil {
			result := githubprivacy.CheckWith(githubJar)
			if !result.OK {
				rep.Skipped = append(rep.Skipped, b.Name+" (GitHub): "+result.Reason)
			} else {
				mergeGitHub(accounts.get("github", result.Login), result, b.Name)
				if result.Reason != "" {
					rep.Skipped = append(rep.Skipped, b.Name+" (GitHub): "+result.Reason)
				}
			}
		}
	}

	rep.Accounts = accounts.accounts()
	return rep
}

func mergeGitHub(account *Account, result githubprivacy.Result, source string) {
	account.Sources = appendUnique(account.Sources, source)
	if result.License != "" {
		account.Plan = result.License
	}
	if account.GitHubCopilot == nil {
		account.GitHubCopilot = &githubprivacy.Settings{}
	}
	if result.Settings.ModelTraining != nil {
		account.GitHubCopilot.ModelTraining = result.Settings.ModelTraining
	}
	if result.Settings.CloudAgentRepositories != "" {
		account.GitHubCopilot.CloudAgentRepositories = result.Settings.CloudAgentRepositories
		account.GitHubCopilot.SelectedRepositories = result.Settings.SelectedRepositories
	}
	if result.Settings.PartnerAgents != nil {
		if account.GitHubCopilot.PartnerAgents == nil {
			account.GitHubCopilot.PartnerAgents = map[string]*bool{}
		}
		for name, enabled := range result.Settings.PartnerAgents {
			account.GitHubCopilot.PartnerAgents[name] = enabled
		}
	}
}

func truthy(b *bool) bool { return b != nil && *b }

func contains(values []string, value string) bool {
	for _, existing := range values {
		if existing == value {
			return true
		}
	}
	return false
}

func appendUnique(values []string, value string) []string {
	if contains(values, value) {
		return values
	}
	return append(values, value)
}

func hasBlocked(values []Blocked, source string) bool {
	for _, value := range values {
		if value.Source == source {
			return true
		}
	}
	return false
}

func appendSharedUnique(values, additions []claude.SharedConversation) []claude.SharedConversation {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		seen[value.URL] = true
	}
	for _, addition := range additions {
		if seen[addition.URL] {
			continue
		}
		seen[addition.URL] = true
		values = append(values, addition)
	}
	return values
}

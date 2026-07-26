// Package account aggregates CLI and browser sources into account-centric
// OpenAI Account and Claude Account views.
package account

import (
	"errors"
	"sort"

	"github.com/circlesac/nosnitch-cli/internal/chatgpt"
	"github.com/circlesac/nosnitch-cli/internal/claude"
	"github.com/circlesac/nosnitch-cli/internal/codex"
	"github.com/circlesac/nosnitch-cli/internal/cookies"
)

type Account struct {
	Provider            string                      `json:"provider"`
	Email               string                      `json:"email"`
	Plan                string                      `json:"plan,omitempty"`
	Sources             []string                    `json:"sources"`
	APIDataSharing      *bool                       `json:"api_data_sharing,omitempty"`
	Training            map[string]*bool            `json:"training,omitempty"` // feature key -> value
	ModelImprovement    *bool                       `json:"model_improvement,omitempty"`
	SharedConversations []claude.SharedConversation `json:"shared_conversations,omitempty"`
	SharedChatsChecked  bool                        `json:"shared_chats_checked,omitempty"`
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
	return truthy(a.ModelImprovement) || len(a.SharedConversations) > 0
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
	for _, a := range r.Accounts {
		if a.Provider == "anthropic" &&
			(a.ModelImprovement == nil || !a.SharedChatsChecked) {
			return true
		}
	}
	return false
}

// Gather reads supported CLI and browser sessions and groups them by provider
// plus email, so the same account discovered through multiple sources is merged.
func Gather() Report {
	byEmail := map[string]*Account{}
	order := []string{}
	get := func(provider, email string) *Account {
		key := provider + "\x00" + email
		if a, ok := byEmail[key]; ok {
			return a
		}
		a := &Account{Provider: provider, Email: email}
		byEmail[key] = a
		order = append(order, key)
		return a
	}

	var rep Report

	if cx := codex.Check(); cx.OK {
		a := get("openai", cx.Email)
		a.Plan = cx.Plan
		a.Sources = append(a.Sources, "Codex CLI")
		v := cx.APIDataSharing
		a.APIDataSharing = &v
	}

	if cc := claude.CheckCode(); cc.OK {
		a := get("anthropic", cc.Email)
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
				a := get("openai", res.Email)
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
			continue
		}
		if err != nil || claudeJar == nil {
			continue
		}
		claudeRes := claude.CheckWeb(claudeJar)
		if !claudeRes.OK {
			rep.Skipped = append(rep.Skipped, b.Name+" (Claude): "+claudeRes.Reason)
			continue
		}
		a := get("anthropic", claudeRes.Email)
		a.Sources = appendUnique(a.Sources, b.Name)
		if claudeRes.ModelImprovement != nil {
			a.ModelImprovement = claudeRes.ModelImprovement
		}
		a.SharedConversations = append(a.SharedConversations, claudeRes.SharedConversations...)
		a.SharedChatsChecked = true
	}

	sort.Strings(order)
	for _, e := range order {
		rep.Accounts = append(rep.Accounts, byEmail[e])
	}
	return rep
}

func truthy(b *bool) bool { return b != nil && *b }

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
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

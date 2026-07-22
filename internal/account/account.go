// Package account aggregates every source (Codex CLI + each browser) into a
// per-account view: one ChatGPT account, the places it's signed in, and its
// training/data-sharing settings.
package account

import (
	"errors"
	"sort"

	"github.com/circlesac/nosnitch-cli/internal/chatgpt"
	"github.com/circlesac/nosnitch-cli/internal/codex"
	"github.com/circlesac/nosnitch-cli/internal/cookies"
)

type Account struct {
	Email          string           `json:"email"`
	Plan           string           `json:"plan,omitempty"`
	Sources        []string         `json:"sources"`
	APIDataSharing *bool            `json:"api_data_sharing,omitempty"`
	Training       map[string]*bool `json:"training,omitempty"` // feature key -> value
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
	return false
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

// Gather reads Codex CLI + every installed browser and groups by account.
func Gather() Report {
	byEmail := map[string]*Account{}
	order := []string{}
	get := func(email string) *Account {
		if a, ok := byEmail[email]; ok {
			return a
		}
		a := &Account{Email: email}
		byEmail[email] = a
		order = append(order, email)
		return a
	}

	var rep Report

	if cx := codex.Check(); cx.OK {
		a := get(cx.Email)
		a.Plan = cx.Plan
		a.Sources = append(a.Sources, "Codex CLI")
		v := cx.APIDataSharing
		a.APIDataSharing = &v
	}

	for _, b := range cookies.Installed() {
		jar, err := b.ChatGPT()
		if errors.Is(err, cookies.ErrNeedFullDiskAccess) {
			rep.Blocked = append(rep.Blocked, Blocked{b.Name, "needs Full Disk Access"})
			continue
		}
		if err != nil || jar == nil {
			continue // no session, or a soft read error — skip quietly
		}
		res := chatgpt.CheckWith(jar)
		if !res.OK {
			rep.Skipped = append(rep.Skipped, b.Name+": "+res.Reason)
			continue
		}
		a := get(res.Email)
		a.Sources = append(a.Sources, b.Name)
		if a.Training == nil {
			a.Training = map[string]*bool{}
		}
		for k, v := range res.Training {
			if v != nil {
				a.Training[k] = v
			}
		}
	}

	sort.Strings(order)
	for _, e := range order {
		rep.Accounts = append(rep.Accounts, byEmail[e])
	}
	return rep
}

func truthy(b *bool) bool { return b != nil && *b }

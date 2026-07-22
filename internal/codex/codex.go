// Package codex reads the local Codex CLI credentials (~/.codex/auth.json) and
// reports the signed-in account, plan, and whether the org is opted into the
// OpenAI API data-sharing (incentives) program — all from the local JWT, no network.
package codex

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const incentivesGroup = "api-data-sharing-incentives-program"

type Result struct {
	OK             bool   `json:"ok"`
	Reason         string `json:"reason,omitempty"`
	Email          string `json:"email,omitempty"`
	Plan           string `json:"plan,omitempty"`
	LastRefresh    string `json:"last_refresh,omitempty"`
	APIDataSharing bool   `json:"api_data_sharing"`
}

// Risk is true when a setting means this account's data can reach model training.
func (r Result) Risk() bool { return r.APIDataSharing }

func Check() Result {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		home = filepath.Join(os.Getenv("HOME"), ".codex")
	}
	path := filepath.Join(home, "auth.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{Reason: "auth.json not found: " + path + " (run: codex login)"}
	}

	var auth struct {
		LastRefresh string `json:"last_refresh"`
		Tokens      struct {
			IDToken string `json:"id_token"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(data, &auth); err != nil {
		return Result{Reason: "auth.json parse error: " + err.Error()}
	}

	claims := jwtClaims(auth.Tokens.IDToken)
	oa, _ := claims["https://api.openai.com/auth"].(map[string]any)
	email, _ := claims["email"].(string)
	plan, _ := oa["chatgpt_plan_type"].(string)

	sharing := false
	if groups, ok := oa["groups"].([]any); ok {
		for _, g := range groups {
			if s, _ := g.(string); s == incentivesGroup {
				sharing = true
			}
		}
	}

	return Result{
		OK:             true,
		Email:          email,
		Plan:           plan,
		LastRefresh:    auth.LastRefresh,
		APIDataSharing: sharing,
	}
}

// jwtClaims decodes a JWT payload (no signature verification — local read only).
func jwtClaims(tok string) map[string]any {
	parts := strings.Split(tok, ".")
	if len(parts) < 2 {
		return map[string]any{}
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	json.Unmarshal(raw, &m)
	return m
}

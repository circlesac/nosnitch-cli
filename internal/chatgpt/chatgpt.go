// Package chatgpt reads the ChatGPT web account's training/data-control flags by
// borrowing the browser's logged-in session: it decrypts Chrome's chatgpt.com
// cookies, impersonates a Chrome/Safari TLS fingerprint (to pass Cloudflare),
// exchanges the session cookie for an accessToken, then reads /backend-api/settings/user.
package chatgpt

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"

	"github.com/circlesac/nosnitch-cli/internal/cookies"
)

// Fingerprints observed to pass chatgpt.com's Cloudflare (Safari first — widest
// headroom; newer Chrome PSK profiles get challenged). Tried in order until one
// returns a valid session.
var passProfiles = []string{
	"safari_ios_17_0", "safari_16_0", "safari_ios_18_5",
	"chrome_117", "chrome_112", "chrome_146",
}

const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

type Result struct {
	OK                   bool   `json:"ok"`
	Reason               string `json:"reason,omitempty"`
	Email                string `json:"email,omitempty"`
	Profile              string `json:"profile,omitempty"`
	TrainingAllowed      *bool  `json:"training_allowed,omitempty"`      // "Improve the model for everyone"
	CodexTrainingAllowed *bool  `json:"codex_training_allowed,omitempty"` // Codex content used for training
}

func (r Result) Risk() bool {
	return (r.TrainingAllowed != nil && *r.TrainingAllowed) ||
		(r.CodexTrainingAllowed != nil && *r.CodexTrainingAllowed)
}

func Check() Result {
	jar, err := cookies.Chrome("chatgpt.com")
	if err != nil {
		return Result{Reason: "Chrome cookie read failed: " + err.Error()}
	}
	_, has0 := jar["__Secure-next-auth.session-token.0"]
	_, hasFull := jar["__Secure-next-auth.session-token"]
	if !has0 && !hasFull {
		return Result{Reason: "no chatgpt.com login session in Chrome (sign in at chatgpt.com)"}
	}
	cookieHeader := buildCookieHeader(jar)

	client, access, email, profile := findSession(cookieHeader)
	if access == "" {
		return Result{Reason: "Cloudflare block or expired session (no accessToken)"}
	}

	status, body := get(client, "https://chatgpt.com/backend-api/settings/user", cookieHeader, access)
	if status != 200 {
		return Result{Reason: "settings read failed (HTTP " + strconv.Itoa(status) + ")",
			Email: email, Profile: profile}
	}

	var payload struct {
		Settings map[string]any `json:"settings"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return Result{Reason: "settings parse error: " + err.Error(), Email: email, Profile: profile}
	}

	res := Result{OK: true, Email: email, Profile: profile}
	if v, ok := payload.Settings["training_allowed"].(bool); ok {
		res.TrainingAllowed = &v
	}
	// prefer the v2 flag, fall back to v1
	if v, ok := payload.Settings["codex_training_allowed_v2"].(bool); ok {
		res.CodexTrainingAllowed = &v
	} else if v, ok := payload.Settings["codex_training_allowed"].(bool); ok {
		res.CodexTrainingAllowed = &v
	}
	return res
}

// findSession tries each fingerprint until one gets a valid session accessToken.
func findSession(cookieHeader string) (client tls_client.HttpClient, access, email, profile string) {
	for _, name := range passProfiles {
		prof, ok := profiles.MappedTLSClients[name]
		if !ok {
			continue
		}
		c, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(),
			tls_client.WithTimeoutSeconds(25), tls_client.WithClientProfile(prof))
		if err != nil {
			continue
		}
		status, body := get(c, "https://chatgpt.com/api/auth/session", cookieHeader, "")
		if status != 200 {
			continue
		}
		var sess struct {
			User        struct{ Email string `json:"email"` } `json:"user"`
			AccessToken string                                `json:"accessToken"`
		}
		if json.Unmarshal([]byte(body), &sess) != nil || sess.AccessToken == "" {
			continue
		}
		return c, sess.AccessToken, sess.User.Email, name
	}
	return nil, "", "", ""
}

func get(c tls_client.HttpClient, url, cookieHeader, bearer string) (int, string) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, ""
	}
	req.Header = http.Header{
		"accept":            {"*/*"},
		"accept-language":   {"en-US,en;q=0.9"},
		"referer":           {"https://chatgpt.com/"},
		"user-agent":        {userAgent},
		"cookie":            {cookieHeader},
		http.HeaderOrderKey: {"accept", "accept-language", "referer", "user-agent", "authorization", "cookie"},
	}
	if bearer != "" {
		req.Header.Set("authorization", "Bearer "+bearer)
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func buildCookieHeader(jar map[string]string) string {
	first := true
	var sb []byte
	for k, v := range jar {
		if !first {
			sb = append(sb, "; "...)
		}
		sb = append(sb, fmt.Sprintf("%s=%s", k, v)...)
		first = false
	}
	return string(sb)
}

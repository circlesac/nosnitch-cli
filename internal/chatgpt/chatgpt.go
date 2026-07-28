// Package chatgpt reads the ChatGPT web account's training/data-control flags by
// borrowing the browser's logged-in session: it decrypts Chrome's chatgpt.com
// cookies, impersonates a Chrome/Safari TLS fingerprint (to pass Cloudflare),
// exchanges the session cookie for an accessToken, then reads /backend-api/settings/user.
package chatgpt

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
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

// Feature is one "your data is used for training" toggle. The same list drives
// both the status read and the `off` write, so they never disagree.
type Feature struct {
	Key    string // backend setting key
	Label  string // report label (sentence case)
	OnNote string // shown when it's ON
}

const CodexTrainingFeatureKey = "codex_training_allowed_v2"

var TrainingFeatures = []Feature{
	{"training_allowed", "Model training", "chats used for training"},
	{"voice_training_allowed", "Voice training", "voice used for training"},
	{"video_training_allowed", "Video training", "video used for training"},
	{CodexTrainingFeatureKey, "Codex training", "Codex content used for training"},
}

type Result struct {
	OK       bool             `json:"ok"`
	Reason   string           `json:"reason,omitempty"`
	Email    string           `json:"email,omitempty"`
	Profile  string           `json:"profile,omitempty"`
	Training map[string]*bool `json:"training,omitempty"` // feature key -> value (nil = not present)
}

// CheckWith reads training/data-control flags for the account whose chatgpt.com
// cookies are in jar (from any browser).
func CheckWith(jar map[string]string) Result {
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

	res := Result{OK: true, Email: email, Profile: profile, Training: map[string]*bool{}}
	for _, f := range TrainingFeatures {
		if v, ok := payload.Settings[f.Key].(bool); ok {
			vv := v
			res.Training[f.Key] = &vv
		}
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
			User struct {
				Email string `json:"email"`
			} `json:"user"`
			AccessToken string `json:"accessToken"`
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

// ── opt-out (write) ──
//
// Endpoint + query-param shape captured from the web app's own request:
//
//	PATCH /backend-api/settings/account_user_setting?feature=<f>&value=false
//
// Auth is the same-origin session cookie plus a Bearer accessToken; it needs the
// account UUID in a ChatGPT-Account-ID header. Features come from TrainingFeatures.

// Captured from the web app; the backend tolerates a slightly stale value.
const oaiClientVersion = "prod-7fc3ff5bcd034a91578eeeb94258b0210e7ff3b2"

type OffResult struct {
	OK      bool              `json:"ok"`
	Reason  string            `json:"reason,omitempty"`
	Email   string            `json:"email,omitempty"`
	Results map[string]string `json:"results,omitempty"` // feature -> "off" | "failed (...)"
}

func (r OffResult) AllOff() bool {
	if !r.OK || len(r.Results) == 0 {
		return false
	}
	for _, v := range r.Results {
		if v != "off" {
			return false
		}
	}
	return true
}

// OffWith opts the account in jar out of every training flag.
func OffWith(jar map[string]string) OffResult {
	cookieHeader := buildCookieHeader(jar)
	client, access, email, _ := findSession(cookieHeader)
	if access == "" {
		return OffResult{Reason: "Cloudflare block or expired session"}
	}
	accountID := jwtAccountID(access)
	if accountID == "" {
		return OffResult{Reason: "could not read account id from session", Email: email}
	}

	res := OffResult{OK: true, Email: email, Results: map[string]string{}}
	for _, f := range TrainingFeatures {
		status, body := patchSetting(client, cookieHeader, accountID, jar["oai-did"], access, f.Key)
		if status == 200 && strings.Contains(body, `"`+f.Key+`":false`) {
			res.Results[f.Key] = "off"
		} else {
			res.Results[f.Key] = fmt.Sprintf("failed (HTTP %d)", status)
		}
	}
	return res
}

func patchSetting(c tls_client.HttpClient, cookieHeader, accountID, deviceID, bearer, feature string) (int, string) {
	url := fmt.Sprintf(
		"https://chatgpt.com/backend-api/settings/account_user_setting?feature=%s&value=false", feature)
	req, err := http.NewRequest(http.MethodPatch, url, nil)
	if err != nil {
		return 0, ""
	}
	req.Header = http.Header{
		"accept":             {"*/*"},
		"accept-language":    {"en-US,en;q=0.9"},
		"authorization":      {"Bearer " + bearer},
		"referer":            {"https://chatgpt.com/"},
		"user-agent":         {userAgent},
		"chatgpt-account-id": {accountID},
		"oai-device-id":      {deviceID},
		"oai-client-version": {oaiClientVersion},
		"cookie":             {cookieHeader},
		http.HeaderOrderKey: {"accept", "accept-language", "authorization", "referer", "user-agent",
			"chatgpt-account-id", "oai-device-id", "oai-client-version", "cookie"},
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func jwtAccountID(tok string) string {
	parts := strings.Split(tok, ".")
	if len(parts) < 2 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	if oa, ok := m["https://api.openai.com/auth"].(map[string]any); ok {
		if id, ok := oa["chatgpt_account_id"].(string); ok {
			return id
		}
	}
	return ""
}

// Package claude inspects a Claude account through Claude Code's local account
// metadata/OAuth token and a browser's logged-in claude.ai session.
package claude

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

const (
	apiBase         = "https://api.anthropic.com"
	webBase         = "https://claude.ai"
	keychainService = "Claude Code-credentials"
	userAgent       = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
		"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
)

type SharedConversation struct {
	Name string `json:"name,omitempty"`
	URL  string `json:"url,omitempty"`
}

type CodeResult struct {
	OK               bool   `json:"ok"`
	Reason           string `json:"reason,omitempty"`
	Email            string `json:"email,omitempty"`
	Plan             string `json:"plan,omitempty"`
	OrganizationUUID string `json:"organization_uuid,omitempty"`
	ModelImprovement *bool  `json:"model_improvement,omitempty"`
}

type WebResult struct {
	OK                  bool                 `json:"ok"`
	Reason              string               `json:"reason,omitempty"`
	Email               string               `json:"email,omitempty"`
	ModelImprovement    *bool                `json:"model_improvement,omitempty"`
	SharedConversations []SharedConversation `json:"shared_conversations,omitempty"`
}

type localAccount struct {
	OAuthAccount struct {
		EmailAddress     string `json:"emailAddress"`
		OrganizationUUID string `json:"organizationUuid"`
		OrganizationType string `json:"organizationType"`
	} `json:"oauthAccount"`
}

// CheckCode reads Claude Code's account metadata and asks Anthropic's read-only
// OAuth settings endpoint for the account-wide model-improvement preference.
func CheckCode() CodeResult {
	home := os.Getenv("CLAUDE_CONFIG_DIR")
	var path string
	if home != "" {
		path = filepath.Join(home, ".claude.json")
	} else {
		path = filepath.Join(os.Getenv("HOME"), ".claude.json")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return CodeResult{Reason: ".claude.json not found: " + path + " (run: claude /login)"}
	}
	var local localAccount
	if err := json.Unmarshal(data, &local); err != nil {
		return CodeResult{Reason: ".claude.json parse error: " + err.Error()}
	}
	email := local.OAuthAccount.EmailAddress
	if email == "" {
		return CodeResult{Reason: "Claude Code OAuth account not found in " + path}
	}
	res := CodeResult{
		OK:               true,
		Email:            email,
		Plan:             planName(local.OAuthAccount.OrganizationType),
		OrganizationUUID: local.OAuthAccount.OrganizationUUID,
	}

	token, err := oauthToken()
	if err != nil {
		res.Reason = err.Error()
		return res
	}
	req, _ := http.NewRequest(http.MethodGet, apiBase+"/api/oauth/account/settings", nil)
	req.Header = http.Header{
		"accept":            {"application/json"},
		"authorization":     {"Bearer " + token},
		"anthropic-version": {"2023-06-01"},
		"user-agent":        {userAgent},
	}
	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(),
		tls_client.WithTimeoutSeconds(25), tls_client.WithClientProfile(profiles.Chrome_131))
	if err != nil {
		res.Reason = "could not create HTTP client: " + err.Error()
		return res
	}
	resp, err := client.Do(req)
	if err != nil {
		res.Reason = "Claude account settings read failed: " + err.Error()
		return res
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		res.Reason = "Claude account settings read failed (HTTP " + strconv.Itoa(resp.StatusCode) + ")"
		return res
	}
	var settings struct {
		GroveEnabled *bool `json:"grove_enabled"`
	}
	if err := json.Unmarshal(body, &settings); err != nil {
		res.Reason = "Claude account settings parse error: " + err.Error()
		return res
	}
	res.ModelImprovement = settings.GroveEnabled
	return res
}

// CheckWeb reads account identity and public shared chats through the same
// read-only endpoints used by claude.ai.
func CheckWeb(jar map[string]string) WebResult {
	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(),
		tls_client.WithTimeoutSeconds(25), tls_client.WithClientProfile(profiles.Chrome_131))
	if err != nil {
		return WebResult{Reason: "could not create HTTP client: " + err.Error()}
	}
	cookie := cookieHeader(jar)
	status, body := webGet(client, "/api/account", cookie)
	if status != 200 {
		return WebResult{Reason: "Claude session expired or blocked (HTTP " + strconv.Itoa(status) + ")"}
	}
	var acct struct {
		EmailAddress string `json:"email_address"`
		Settings     struct {
			GroveEnabled *bool `json:"grove_enabled"`
		} `json:"settings"`
		Memberships []struct {
			Organization struct {
				UUID string `json:"uuid"`
			} `json:"organization"`
		} `json:"memberships"`
	}
	if err := json.Unmarshal(body, &acct); err != nil {
		return WebResult{Reason: "Claude account parse error: " + err.Error()}
	}
	res := WebResult{OK: true, Email: acct.EmailAddress, ModelImprovement: acct.Settings.GroveEnabled}
	orgs := map[string]bool{}
	for _, membership := range acct.Memberships {
		if org := membership.Organization.UUID; org != "" {
			orgs[org] = true
		}
	}
	// The account payload has changed shape over time. The organizations endpoint
	// is the authoritative fallback and is also used by the current web app.
	if orgStatus, orgBody := webGet(client, "/api/organizations", cookie); orgStatus == 200 {
		var organizations []struct {
			UUID string `json:"uuid"`
		}
		if json.Unmarshal(orgBody, &organizations) == nil {
			for _, org := range organizations {
				if org.UUID != "" {
					orgs[org.UUID] = true
				}
			}
		}
	}
	for org := range orgs {
		status, body = webGet(client, "/api/organizations/"+org+"/shares", cookie)
		if status != 200 {
			res.OK = false
			res.Reason = "Claude shared conversations read failed (HTTP " + strconv.Itoa(status) + ")"
			continue
		}
		res.SharedConversations = append(res.SharedConversations, parseShares(body)...)
	}
	return res
}

func oauthToken() (string, error) {
	out, err := exec.Command("security", "find-generic-password", "-w", "-s", keychainService).Output()
	if err != nil {
		return "", fmt.Errorf("Claude Code credentials not found in Keychain")
	}
	var creds struct {
		ClaudeAIOAuth struct {
			AccessToken string `json:"accessToken"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(out, &creds); err != nil || creds.ClaudeAIOAuth.AccessToken == "" {
		return "", fmt.Errorf("Claude Code OAuth token could not be read")
	}
	return creds.ClaudeAIOAuth.AccessToken, nil
}

func planName(orgType string) string {
	orgType = strings.TrimPrefix(orgType, "claude_")
	return strings.ReplaceAll(orgType, "_", " ")
}

func webGet(client tls_client.HttpClient, path, cookie string) (int, []byte) {
	req, err := http.NewRequest(http.MethodGet, webBase+path, nil)
	if err != nil {
		return 0, nil
	}
	req.Header = http.Header{
		"accept":            {"application/json"},
		"referer":           {webBase + "/chats"},
		"user-agent":        {userAgent},
		"cookie":            {cookie},
		http.HeaderOrderKey: {"accept", "referer", "user-agent", "cookie"},
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

func cookieHeader(jar map[string]string) string {
	parts := make([]string, 0, len(jar))
	for k, v := range jar {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, "; ")
}

func parseShares(body []byte) []SharedConversation {
	var rows []map[string]any
	if json.Unmarshal(body, &rows) != nil {
		var wrapped struct {
			Data []map[string]any `json:"data"`
		}
		if json.Unmarshal(body, &wrapped) != nil {
			return nil
		}
		rows = wrapped.Data
	}
	out := make([]SharedConversation, 0, len(rows))
	for _, row := range rows {
		name := firstString(row, "name", "title", "conversation_name")
		url := firstString(row, "url", "share_url")
		if url == "" {
			if uuid := firstString(row, "snapshot_uuid", "share_uuid", "uuid", "id"); uuid != "" {
				url = webBase + "/share/" + uuid
			}
		}
		out = append(out, SharedConversation{Name: name, URL: url})
	}
	return out
}

func firstString(row map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := row[key].(string); ok {
			return value
		}
	}
	return ""
}

// Package github reads GitHub Copilot privacy and coding-agent settings from
// a signed-in github.com browser session.
package github

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const (
	webBase         = "https://github.com"
	featuresPath    = "/settings/copilot/features"
	codingAgentPath = "/settings/copilot/coding_agent"
	userAgent       = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
		"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
)

type Settings struct {
	ModelTraining          *bool            `json:"model_training,omitempty"`
	CloudAgentRepositories string           `json:"cloud_agent_repositories,omitempty"`
	SelectedRepositories   int              `json:"selected_repositories,omitempty"`
	PartnerAgents          map[string]*bool `json:"partner_agents,omitempty"`
}

type Result struct {
	OK       bool     `json:"ok"`
	Reason   string   `json:"reason,omitempty"`
	Login    string   `json:"login,omitempty"`
	License  string   `json:"license,omitempty"`
	Settings Settings `json:"settings"`
}

func (r Result) Complete() bool {
	return r.OK && r.License != "" && r.Settings.ModelTraining != nil &&
		r.Settings.CloudAgentRepositories != "" && completePartnerAgents(r.Settings.PartnerAgents)
}

func (r Result) Risk() bool {
	return r.Settings.ModelTraining != nil && *r.Settings.ModelTraining
}

type OffResult struct {
	OK      bool   `json:"ok"`
	Reason  string `json:"reason,omitempty"`
	Login   string `json:"login,omitempty"`
	Changed bool   `json:"changed"`
}

type doer interface {
	Do(*http.Request) (*http.Response, error)
}

type featuresPage struct {
	Login             string
	License           string
	ModelTraining     *bool
	FormAction        string
	AuthenticityToken string
}

type codingAgentPage struct {
	RepositoryScope string
	SelectedCount   int
	PartnerAgents   map[string]*bool
}

func CheckWith(jar map[string]string) Result {
	return checkWith(&http.Client{Timeout: 25 * time.Second}, webBase, jar)
}

func checkWith(client doer, base string, jar map[string]string) Result {
	cookie := cookieHeader(jar)
	status, body, err := get(client, base+featuresPath, cookie)
	if err != nil {
		return Result{Reason: "GitHub Copilot settings read failed: " + err.Error()}
	}
	if status != http.StatusOK {
		return Result{Reason: "GitHub Copilot settings read failed (HTTP " + strconv.Itoa(status) + ")"}
	}
	features, err := parseFeatures(body)
	if err != nil || features.Login == "" {
		reason := "signed-in GitHub account not found"
		if err != nil {
			reason = err.Error()
		}
		return Result{Reason: reason}
	}

	result := Result{
		OK:      true,
		Login:   features.Login,
		License: features.License,
		Settings: Settings{
			ModelTraining: features.ModelTraining,
		},
	}
	var reasons []string
	if features.License == "" {
		reasons = append(reasons, "Copilot license could not be read")
	}
	if features.ModelTraining == nil {
		reasons = append(reasons, "Copilot model-training preference could not be read")
	}

	status, body, err = get(client, base+codingAgentPath, cookie)
	if err != nil {
		reasons = append(reasons, "Copilot cloud-agent settings read failed: "+err.Error())
	} else if status != http.StatusOK {
		reasons = append(reasons, "Copilot cloud-agent settings read failed (HTTP "+strconv.Itoa(status)+")")
	} else {
		coding, parseErr := parseCodingAgent(body)
		result.Settings.CloudAgentRepositories = coding.RepositoryScope
		result.Settings.SelectedRepositories = coding.SelectedCount
		result.Settings.PartnerAgents = coding.PartnerAgents
		if parseErr != nil {
			reasons = append(reasons, parseErr.Error())
		}
	}
	result.Reason = strings.Join(reasons, "; ")
	return result
}

func OffWith(jar map[string]string) OffResult {
	return offWith(&http.Client{Timeout: 25 * time.Second}, webBase, jar)
}

func offWith(client doer, base string, jar map[string]string) OffResult {
	cookie := cookieHeader(jar)
	status, body, err := get(client, base+featuresPath, cookie)
	if err != nil {
		return OffResult{Reason: "GitHub Copilot settings read failed: " + err.Error()}
	}
	if status != http.StatusOK {
		return OffResult{Reason: "GitHub Copilot settings read failed (HTTP " + strconv.Itoa(status) + ")"}
	}
	features, err := parseFeatures(body)
	if err != nil || features.Login == "" {
		return OffResult{Reason: "signed-in GitHub account not found"}
	}
	if features.ModelTraining == nil {
		return OffResult{Login: features.Login, Reason: "Copilot model-training preference could not be read"}
	}
	if !*features.ModelTraining {
		return OffResult{OK: true, Login: features.Login}
	}
	if features.AuthenticityToken == "" || features.FormAction != "/settings/copilot" {
		return OffResult{Login: features.Login, Reason: "Copilot model-training update form could not be read"}
	}

	form := url.Values{
		"_method":            {"put"},
		"authenticity_token": {features.AuthenticityToken},
		"telemetry":          {"disabled"},
	}
	updateStatus, _, err := postForm(client, strings.TrimRight(base, "/")+features.FormAction, base+featuresPath, cookie, form)
	if err != nil {
		return OffResult{Login: features.Login, Reason: "Copilot model-training update failed: " + err.Error()}
	}

	status, body, err = get(client, base+featuresPath, cookie)
	if err != nil || status != http.StatusOK {
		if updateStatus < 200 || updateStatus >= 400 {
			return OffResult{Login: features.Login, Reason: "Copilot model-training update failed (HTTP " + strconv.Itoa(updateStatus) + "); verification failed"}
		}
		return OffResult{Login: features.Login, Reason: "Copilot model-training verification failed"}
	}
	verified, err := parseFeatures(body)
	if err != nil || verified.ModelTraining == nil {
		if updateStatus < 200 || updateStatus >= 400 {
			return OffResult{Login: features.Login, Reason: "Copilot model-training update failed (HTTP " + strconv.Itoa(updateStatus) + "); verification failed"}
		}
		return OffResult{Login: features.Login, Reason: "Copilot model-training verification failed"}
	}
	if *verified.ModelTraining {
		if updateStatus < 200 || updateStatus >= 400 {
			return OffResult{Login: features.Login, Reason: "Copilot model-training update failed (HTTP " + strconv.Itoa(updateStatus) + ")"}
		}
		return OffResult{Login: features.Login, Reason: "Copilot model-training preference is still enabled"}
	}
	return OffResult{OK: true, Login: features.Login, Changed: true}
}

func parseFeatures(body []byte) (featuresPage, error) {
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return featuresPage{}, fmt.Errorf("GitHub Copilot settings parse error: %w", err)
	}
	var page featuresPage
	planPattern := regexp.MustCompile(`^GitHub Copilot (.+) is active for your account$`)
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "meta" && attr(node, "name") == "user-login" {
			page.Login = attr(node, "content")
		}
		if node.Type == html.TextNode {
			text := strings.Join(strings.Fields(node.Data), " ")
			if match := planPattern.FindStringSubmatch(text); len(match) == 2 {
				page.License = "Copilot " + match[1]
			}
		}
		if node.Type == html.ElementNode && node.Data == "form" &&
			attr(node, "action") == "/settings/copilot" && page.AuthenticityToken == "" {
			if telemetry := descendantInput(node, "telemetry"); telemetry != nil {
				if setting := settingBool(attr(telemetry, "value")); setting != nil {
					page.ModelTraining = setting
					page.FormAction = attr(node, "action")
					if token := descendantInput(node, "authenticity_token"); token != nil {
						page.AuthenticityToken = attr(token, "value")
					}
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	if page.Login == "" {
		return page, fmt.Errorf("signed-in GitHub account not found")
	}
	return page, nil
}

func parseCodingAgent(body []byte) (codingAgentPage, error) {
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return codingAgentPage{}, fmt.Errorf("Copilot cloud-agent settings parse error: %w", err)
	}
	var page codingAgentPage
	foundScope := false
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "script" &&
			attr(node, "type") == "application/json" && node.FirstChild != nil {
			var payload struct {
				Props struct {
					Mode      string            `json:"mode"`
					Selection []json.RawMessage `json:"selection"`
					Agents    []struct {
						Name    string `json:"name"`
						Enabled bool   `json:"enabled"`
					} `json:"agents"`
					ModeChangedCallbackPath string `json:"modeChangedCallbackPath"`
				} `json:"props"`
			}
			if json.Unmarshal([]byte(node.FirstChild.Data), &payload) == nil {
				if payload.Props.Mode != "" || payload.Props.ModeChangedCallbackPath != "" {
					page.RepositoryScope = repositoryScope(payload.Props.Mode)
					page.SelectedCount = len(payload.Props.Selection)
					foundScope = true
				}
				if payload.Props.Agents != nil {
					if page.PartnerAgents == nil {
						page.PartnerAgents = map[string]*bool{}
					}
				}
				for _, agent := range payload.Props.Agents {
					name := agentName(agent.Name)
					if name == "" {
						continue
					}
					enabled := agent.Enabled
					page.PartnerAgents[name] = &enabled
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	if !foundScope || page.RepositoryScope == "" {
		return page, fmt.Errorf("Copilot cloud-agent settings could not be read")
	}
	if !completePartnerAgents(page.PartnerAgents) {
		return page, fmt.Errorf("Copilot partner-agent settings could not be read")
	}
	return page, nil
}

func repositoryScope(mode string) string {
	switch strings.ToLower(mode) {
	case "all", "all_repos", "all_repositories":
		return "all"
	case "selected", "selected_repos", "selected_repositories":
		return "selected"
	case "none", "no_repos", "no_repositories":
		return "none"
	default:
		return ""
	}
}

func agentName(name string) string {
	switch name {
	case "anthropic-code-agent":
		return "claude"
	case "openai-code-agent":
		return "codex"
	}
	return ""
}

func completePartnerAgents(agents map[string]*bool) bool {
	return agents != nil && agents["claude"] != nil && agents["codex"] != nil
}

func settingBool(value string) *bool {
	var result bool
	switch strings.ToLower(value) {
	case "enabled", "true", "allowed":
		result = true
	case "disabled", "false", "blocked":
		result = false
	default:
		return nil
	}
	return &result
}

func descendantInput(node *html.Node, name string) *html.Node {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && child.Data == "input" && attr(child, "name") == name {
			return child
		}
		if found := descendantInput(child, name); found != nil {
			return found
		}
	}
	return nil
}

func attr(node *html.Node, name string) string {
	for _, value := range node.Attr {
		if value.Key == name {
			return value.Val
		}
	}
	return ""
}

func get(client doer, target, cookie string) (int, []byte, error) {
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return 0, nil, err
	}
	requestHeaders(req, cookie)
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

func postForm(client doer, target, referer, cookie string, form url.Values) (int, []byte, error) {
	req, err := http.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, nil, err
	}
	requestHeaders(req, cookie)
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	req.Header.Set("referer", referer)
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

func requestHeaders(req *http.Request, cookie string) {
	req.Header.Set("accept", "text/html,application/xhtml+xml")
	req.Header.Set("user-agent", userAgent)
	req.Header.Set("cookie", cookie)
}

func cookieHeader(jar map[string]string) string {
	parts := make([]string, 0, len(jar))
	for name, value := range jar {
		parts = append(parts, name+"="+value)
	}
	sort.Strings(parts)
	return strings.Join(parts, "; ")
}

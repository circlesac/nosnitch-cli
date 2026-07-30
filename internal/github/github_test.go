package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseFeatures(t *testing.T) {
	page, err := parseFeatures([]byte(`<html><head>
<meta name="user-login" content="octocat">
</head><body>
<p>GitHub Copilot Pro is active for your account</p>
<form action="/settings/copilot" method="post">
<input name="authenticity_token" value="csrf-value">
<input name="_method" value="put">
<input name="telemetry" value="enabled">
</form></body></html>`))
	if err != nil {
		t.Fatal(err)
	}
	if page.Login != "octocat" || page.License != "Copilot Pro" {
		t.Fatalf("parseFeatures() identity = %#v", page)
	}
	if page.ModelTraining == nil || !*page.ModelTraining {
		t.Fatalf("parseFeatures() training = %#v", page.ModelTraining)
	}
	if page.FormAction != "/settings/copilot" || page.AuthenticityToken != "csrf-value" {
		t.Fatalf("parseFeatures() form = %#v", page)
	}
}

func TestParseCodingAgent(t *testing.T) {
	body := codingAgentHTML("all_repos", 0, true, false)
	page, err := parseCodingAgent([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if page.RepositoryScope != "all" || page.SelectedCount != 0 {
		t.Fatalf("parseCodingAgent() repositories = %#v", page)
	}
	if page.PartnerAgents["claude"] == nil || !*page.PartnerAgents["claude"] {
		t.Fatalf("parseCodingAgent() Claude = %#v", page.PartnerAgents)
	}
	if page.PartnerAgents["codex"] == nil || *page.PartnerAgents["codex"] {
		t.Fatalf("parseCodingAgent() Codex = %#v", page.PartnerAgents)
	}
}

func TestCheckWithReportsIncompleteSettings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case featuresPath:
			fmt.Fprint(w, `<meta name="user-login" content="octocat">`)
		case codingAgentPath:
			fmt.Fprint(w, codingAgentHTML("all_repos", 0, false, false))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result := checkWith(server.Client(), server.URL, map[string]string{"session": "not-printed"})
	if !result.OK || result.Complete() {
		t.Fatalf("checkWith() = %#v", result)
	}
	if !strings.Contains(result.Reason, "license") || !strings.Contains(result.Reason, "model-training") {
		t.Fatalf("checkWith() reason = %q", result.Reason)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "not-printed") || strings.Contains(string(encoded), "csrf") {
		t.Fatalf("result leaked credentials: %s", encoded)
	}
}

func TestOffWithChangesOnlyTrainingAndVerifies(t *testing.T) {
	training := true
	var posted bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != featuresPath && r.URL.Path != "/settings/copilot" {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodPost {
			posted = true
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if len(r.PostForm) != 3 || r.PostForm.Get("_method") != "put" ||
				r.PostForm.Get("authenticity_token") != "csrf-value" ||
				r.PostForm.Get("telemetry") != "disabled" {
				t.Fatalf("unexpected update form: %#v", r.PostForm)
			}
			training = false
			w.WriteHeader(http.StatusNoContent)
			return
		}
		value := "disabled"
		if training {
			value = "enabled"
		}
		fmt.Fprintf(w, `<meta name="user-login" content="octocat">
<form action="/settings/copilot"><input name="authenticity_token" value="csrf-value"><input name="telemetry" value="%s"></form>`, value)
	}))
	defer server.Close()

	result := offWith(server.Client(), server.URL, map[string]string{"session": "secret"})
	if !result.OK || !result.Changed || !posted || result.Login != "octocat" {
		t.Fatalf("offWith() = %#v, posted=%v", result, posted)
	}
}

func TestOffWithRejectsUnexpectedFormAction(t *testing.T) {
	var posted bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posted = true
		}
		fmt.Fprint(w, `<meta name="user-login" content="octocat">
<form action="https://example.com/collect"><input name="authenticity_token" value="csrf-value"><input name="telemetry" value="enabled"></form>`)
	}))
	defer server.Close()

	result := offWith(server.Client(), server.URL, map[string]string{"session": "secret"})
	if result.OK || posted || !strings.Contains(result.Reason, "update form") {
		t.Fatalf("offWith() = %#v, posted=%v", result, posted)
	}
}

func TestRepositoryScope(t *testing.T) {
	for input, want := range map[string]string{
		"all_repos": "all", "selected_repos": "selected", "no_repos": "none", "unexpected": "",
	} {
		if got := repositoryScope(input); got != want {
			t.Fatalf("repositoryScope(%q) = %q, want %q", input, got, want)
		}
	}
}

func codingAgentHTML(mode string, selected int, claude, codex bool) string {
	selection := make([]map[string]any, selected)
	repositoryPayload := map[string]any{"props": map[string]any{
		"mode":                    mode,
		"modeChangedCallbackPath": "/settings/copilot/coding_agent/repositories",
		"selection":               selection,
	}}
	agentPayload := map[string]any{"props": map[string]any{
		"third_party_agent_enablement_callback_path": "/settings/copilot/coding_agent/toggle_third_party_agents_enablement",
		"agents": []map[string]any{
			{"name": "anthropic-code-agent", "displayName": "Claude", "enabled": claude},
			{"name": "openai-code-agent", "displayName": "Codex", "enabled": codex},
		},
	}}
	repositoryData, _ := json.Marshal(repositoryPayload)
	agentData, _ := json.Marshal(agentPayload)
	return `<script type="application/json">` + string(repositoryData) + `</script>` +
		`<script type="application/json">` + string(agentData) + `</script>`
}

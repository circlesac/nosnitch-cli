package claude

import (
	"encoding/json"
	"testing"
)

func TestPlanName(t *testing.T) {
	if got := planName("claude_max_20x"); got != "max 20x" {
		t.Fatalf("planName() = %q", got)
	}
}

func TestParseOAuthToken(t *testing.T) {
	got, err := parseOAuthToken([]byte(`{"claudeAiOauth":{"accessToken":"token-1"}}`))
	if err != nil || got != "token-1" {
		t.Fatalf("parseOAuthToken() = %q, %v", got, err)
	}
}

func TestParseShares(t *testing.T) {
	body := []byte(`[
		{"conversation_uuid":"conversation-1","conversation_name":"Secrets","snapshot_uuid":"share-1"},
		{"title":"Public notes","share_url":"https://claude.ai/share/share-2"}
	]`)
	got := parseShares(body, "org-1")
	if len(got) != 2 {
		t.Fatalf("len(parseShares()) = %d", len(got))
	}
	if got[0].Name != "Secrets" || got[0].URL != "https://claude.ai/share/share-1" {
		t.Fatalf("parseShares()[0] = %#v", got[0])
	}
	if got[0].OrganizationUUID != "org-1" || got[0].SnapshotUUID != "share-1" {
		t.Fatalf("parseShares()[0] metadata = %#v", got[0])
	}
	if got[0].ConversationUUID != "conversation-1" {
		t.Fatalf("parseShares()[0].ConversationUUID = %q", got[0].ConversationUUID)
	}
}

func TestParseConversationName(t *testing.T) {
	if got := parseConversationName([]byte(`{"name":"Greeting"}`)); got != "Greeting" {
		t.Fatalf("parseConversationName() = %q", got)
	}
}

func TestParseWrappedShares(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"data": []map[string]any{{"snapshot_uuid": "share-3"}},
	})
	got := parseShares(body, "org-1")
	if len(got) != 1 || got[0].URL != "https://claude.ai/share/share-3" {
		t.Fatalf("parseShares() = %#v", got)
	}
}

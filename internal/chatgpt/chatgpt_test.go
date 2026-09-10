package chatgpt

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildCookieHeaderContainsAllCookies(t *testing.T) {
	header := buildCookieHeader(map[string]string{"session": "value", "device": "id"})
	for _, part := range []string{"session=value", "device=id"} {
		if !strings.Contains(header, part) {
			t.Fatalf("cookie header %q missing %q", header, part)
		}
	}
}

func TestJWTAccountID(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"https://api.openai.com/auth": map[string]string{"chatgpt_account_id": "acct-test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	token := "header." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
	if got := jwtAccountID(token); got != "acct-test" {
		t.Fatalf("jwtAccountID() = %q, want acct-test", got)
	}
}

//go:build linux

package claude

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxOAuthToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"linux-token"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := oauthToken()
	if err != nil || got != "linux-token" {
		t.Fatalf("oauthToken() = %q, %v", got, err)
	}
}

func TestLinuxOAuthTokenUsesClaudeConfigDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"custom-token"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := oauthToken()
	if err != nil || got != "custom-token" {
		t.Fatalf("oauthToken() = %q, %v", got, err)
	}
}

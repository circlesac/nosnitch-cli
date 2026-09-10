package registry

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRegistryUsesXDGPathsAndDoesNotExposeCredentials(t *testing.T) {
	config, cache := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("XDG_CACHE_HOME", cache)
	if err := Upsert(Account{ID: "claude:account-1", Provider: "claude", Email: "alice@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(config, "nosnitch", "accounts.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cache, "nosnitch", "credentials")); !os.IsNotExist(err) {
		t.Fatalf("credentials directory unexpectedly created: %v", err)
	}
	accounts, err := Load()
	if err != nil || len(accounts) != 1 {
		t.Fatalf("Load() = %#v, %v", accounts, err)
	}
}

func TestRemoveDeletesCredentialFile(t *testing.T) {
	config, cache := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("XDG_CACHE_HOME", cache)
	if err := Upsert(Account{ID: "openai:account-1", Provider: "openai"}); err != nil {
		t.Fatal(err)
	}
	p := CredentialPath("openai:account-1")
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Remove("openai:account-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("credential was not removed: %v", err)
	}
}

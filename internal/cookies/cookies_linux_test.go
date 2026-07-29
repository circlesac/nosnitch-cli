//go:build linux

package cookies

import (
	"crypto/sha1"
	"crypto/sha256"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/pbkdf2"
)

func TestLinuxBrowserDiscoveryPrefersNetworkCookies(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".config/chromium/Default/Network/Cookies")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	installed := Installed()
	if len(installed) != 1 || installed[0].Name != "Chromium" || installed[0].path() != path {
		t.Fatalf("Installed() = %#v", installed)
	}
}

func TestLinuxChromiumCookies(t *testing.T) {
	tests := []struct {
		name     string
		scheme   string
		password string
	}{
		{"v10 basic storage", "v10", "peanuts"},
		{"v11 secret service", "v11", "keyring-password"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			if tt.scheme == "v11" {
				binDir := filepath.Join(home, "bin")
				if err := os.MkdirAll(binDir, 0o755); err != nil {
					t.Fatal(err)
				}
				script := "#!/bin/sh\n[ \"$*\" = \"lookup application chromium\" ] || exit 1\nprintf keyring-password"
				if err := os.WriteFile(filepath.Join(binDir, "secret-tool"), []byte(script), 0o755); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			}

			browser := platformBrowsers()[1]
			path := filepath.Join(home, ".config/chromium/Default/Network/Cookies")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("CREATE TABLE meta (key LONGVARCHAR NOT NULL UNIQUE PRIMARY KEY, value LONGVARCHAR); INSERT INTO meta VALUES ('version', '24'); CREATE TABLE cookies (host_key TEXT, name TEXT, value TEXT, encrypted_value BLOB)"); err != nil {
				t.Fatal(err)
			}
			hostKey := ".chatgpt.com"
			digest := sha256.Sum256([]byte(hostKey))
			key := pbkdf2.Key([]byte(tt.password), []byte("saltysalt"), 1, 16, sha1.New)
			if _, err := db.Exec("INSERT INTO cookies VALUES (?, ?, '', ?)", hostKey, "session", encryptCookie(tt.scheme, key, append(digest[:], []byte("cookie-value")...))); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}

			got, err := browser.ChatGPT()
			if err != nil {
				t.Fatal(err)
			}
			if got["session"] != "cookie-value" {
				t.Fatalf("ChatGPT() = %#v", got)
			}
		})
	}
}

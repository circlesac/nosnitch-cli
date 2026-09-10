package registry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLoadSaveUsesXDGAndHidesCredentialPath(t *testing.T) {
	config := t.TempDir()
	cache := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("XDG_CACHE_HOME", cache)

	if err := Upsert(Account{ID: "claude:account-1", Provider: "claude", Email: "alice@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(config, "nosnitch", "accounts.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(CredentialPath("claude:account-1")); !os.IsNotExist(err) {
		t.Fatalf("credential file unexpectedly exists: %v", err)
	}
	accounts, err := Load()
	if err != nil || len(accounts) != 1 {
		t.Fatalf("Load() = %#v, %v", accounts, err)
	}

	hashPath := CredentialPath("claude:account-1")
	base := filepath.Base(hashPath)
	if base == "claude:account-1.json" || strings.Contains(base, "/") || strings.Contains(base, "..") {
		t.Fatalf("credential filename should be hashed: %s", base)
	}
}

func TestPermissionRepairForMetadataAndCredentialCache(t *testing.T) {
	configRoot := t.TempDir()
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(configRoot, "nosnitch-config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(cacheRoot, "nosnitch-cache"))

	rootDir := filepath.Join(configRoot, "nosnitch-config", "nosnitch")
	if err := os.MkdirAll(rootDir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(rootDir, 0o777); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(configRoot, "nosnitch-config", "nosnitch", "accounts.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}

	cacheDir := filepath.Join(cacheRoot, "nosnitch-cache", "nosnitch", "credentials")
	if err := os.MkdirAll(cacheDir, 0o777); err != nil {
		t.Fatal(err)
	}
	credPath := filepath.Join(cacheDir, "cached.json")
	if err := os.WriteFile(credPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
	if err := SaveCredential("acc", Credential{}); err != nil {
		t.Fatal(err)
	}

	if got, err := os.Stat(rootDir); err != nil {
		t.Fatal(err)
	} else if got.Mode().Perm() != 0o700 {
		t.Fatalf("config dir mode = %#o, want %#o", got.Mode().Perm(), 0o700)
	}
	if got, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if got.Mode().Perm() != 0o600 {
		t.Fatalf("config file mode = %#o, want %#o", got.Mode().Perm(), 0o600)
	}
	if got, err := os.Stat(cacheDir); err != nil {
		t.Fatal(err)
	} else if got.Mode().Perm() != 0o700 {
		t.Fatalf("cache dir mode = %#o, want %#o", got.Mode().Perm(), 0o700)
	}
	if got, err := os.Stat(CredentialPath("acc")); err != nil {
		t.Fatal(err)
	} else if got.Mode().Perm() != 0o600 {
		t.Fatalf("credential file mode = %#o, want %#o", got.Mode().Perm(), 0o600)
	}
}

func TestRejectRelativeOrSymlinkXDGRoots(t *testing.T) {
	config := t.TempDir()
	cache := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "relative/path")
	t.Setenv("XDG_CACHE_HOME", cache)
	if _, err := Load(); err == nil {
		t.Fatal("expected relative config root rejection")
	}
	t.Setenv("XDG_CONFIG_HOME", config)
	actual := filepath.Join(config, "real")
	if err := os.MkdirAll(actual, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(actual, filepath.Join(config, "nosnitch")); err != nil {
		t.Fatal(err)
	}
	if err := Save([]Account{}); err == nil {
		t.Fatal("expected symlink config directory rejection")
	}
}

func TestMissingCacheKeepsMetadata(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	if err := Upsert(Account{ID: "openai:account-1", Provider: "openai"}); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCredential("openai:account-1"); err == nil {
		t.Fatal("expected missing credential cache")
	}
	accounts, err := Load()
	if err != nil || len(accounts) != 1 {
		t.Fatalf("Load() = %#v, %v", accounts, err)
	}
}

func TestCorruptConfigFailsClosed(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	path := filepath.Join(config, "nosnitch", "accounts.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{invalid"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(); err == nil {
		t.Fatal("expected corrupt config to fail")
	}
}

func TestConcurrentUpsertsPreserved(t *testing.T) {
	config := t.TempDir()
	cache := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("XDG_CACHE_HOME", cache)

	const count = 80
	var wg sync.WaitGroup
	wg.Add(count)
	for i := 0; i < count; i++ {
		i := i
		go func() {
			defer wg.Done()
			id := "provider:" + strconv.Itoa(i)
			err := Upsert(Account{ID: id, Provider: "p", UpdatedAt: time.Unix(int64(i), 0)})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	accounts, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != count {
		t.Fatalf("expected %d accounts, got %d", count, len(accounts))
	}
}

func TestAtomicOldOrNewReads(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	stop := make(chan struct{})
	var readersWg sync.WaitGroup
	errCh := make(chan error, 1000)
	readersWg.Add(1)
	go func() {
		defer readersWg.Done()
		for i := 0; i < 500; i++ {
			select {
			case <-stop:
				return
			default:
				data, err := Load()
				if err != nil {
					errCh <- err
					return
				}
				_, err = json.Marshal(data)
				if err != nil {
					errCh <- err
					return
				}
			}
		}
	}()

	for i := 0; i < 500; i++ {
		if err := Upsert(Account{ID: "id-" + strconv.Itoa(i%10), Provider: "p"}); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	readersWg.Wait()
	select {
	case err := <-errCh:
		t.Fatal(err)
	default:
	}
}

func TestCredentialRegistrationWritesCacheThenMetadata(t *testing.T) {
	config := t.TempDir()
	cache := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("XDG_CACHE_HOME", cache)

	if err := Register(Account{ID: "provider:myacc", Alias: "main"}, Credential{Kind: "cookie", Cookies: map[string]string{"a": "b"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(CredentialPath("provider:myacc")); err != nil {
		t.Fatal(err)
	}
	if err := Register(Account{ID: "provider:myacc", Alias: "updated"}, Credential{Kind: "cookie", Cookies: map[string]string{"c": "d"}}); err != nil {
		t.Fatal(err)
	}
	accounts, err := Load()
	if err != nil || len(accounts) != 1 {
		t.Fatalf("Load() = %#v, %v", accounts, err)
	}
	if accounts[0].Alias != "updated" {
		t.Fatalf("expected alias preserved/updated, got %q", accounts[0].Alias)
	}
}

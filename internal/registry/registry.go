// Package registry stores explicitly registered provider accounts and their
// short-lived credentials. Account metadata is configuration; credentials are
// kept separately so listing accounts never reads secrets.
package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

const (
	accountLockName = "accounts.lock"
	accountFileName = "accounts.json"
	rootPermission  = 0o700
	filePermission  = 0o600
)

type Account struct {
	ID            string    `json:"id"`
	Provider      string    `json:"provider"`
	Email         string    `json:"email,omitempty"`
	Organization  string    `json:"organization,omitempty"`
	Alias         string    `json:"alias,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
	Subject       string    `json:"subject,omitempty"`
	AuthKind      string    `json:"auth_kind,omitempty"`
	LastCheckedAt time.Time `json:"last_checked_at"`
	LastStatus    string    `json:"last_status,omitempty"`
}

type Credential struct {
	Kind         string            `json:"kind"`
	Cookies      map[string]string `json:"cookies"`
	AccessToken  string            `json:"access_token"`
	RefreshToken string            `json:"refresh_token"`
	IDToken      string            `json:"id_token"`
	ExpiresAt    time.Time         `json:"expires_at"`
}

func xdgRoot(envKey, fallback string) (string, error) {
	root := os.Getenv(envKey)
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(home, fallback)
	}
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("%s must be an absolute path", envKey)
	}
	if info, err := os.Lstat(root); err == nil && info.Mode()&os.ModeSymlink != 0 {
		resolved, resolveErr := filepath.EvalSymlinks(root)
		if resolveErr != nil {
			return "", resolveErr
		}
		targetInfo, statErr := os.Stat(resolved)
		if statErr != nil {
			return "", statErr
		}
		if !targetInfo.IsDir() {
			return "", fmt.Errorf("XDG root is not a directory: %s", root)
		}
	}
	return filepath.Clean(root), nil
}

func configDir() (string, error) {
	root, err := xdgRoot("XDG_CONFIG_HOME", ".config")
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "nosnitch"), nil
}

func cacheDir() (string, error) {
	root, err := xdgRoot("XDG_CACHE_HOME", ".cache")
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "nosnitch", "credentials"), nil
}

func configPath() (string, error) {
	root, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, accountFileName), nil
}

func hashedAccountID(id string) string {
	h := sha256.Sum256([]byte(id))
	return hex.EncodeToString(h[:])
}

func credentialPath(id string) string {
	rawCache, err := cacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(rawCache, hashedAccountID(id)+".json")
}

func CredentialPath(id string) string {
	return credentialPath(id)
}

func validateNoSymlink(path string) error {
	if path == "" {
		return errors.New("path is empty")
	}
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return fmt.Errorf("path is relative: %s", clean)
	}
	if _, err := os.Lstat(clean); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	info, _ := os.Lstat(clean)
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("path is a symlink: %s", clean)
	}
	return nil
}

func ensureDirectory(path string) error {
	if err := os.MkdirAll(path, rootPermission); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", path)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("path is a symlink: %s", path)
	}
	if info.Mode().Perm() != rootPermission {
		if err := os.Chmod(path, rootPermission); err != nil {
			return err
		}
	}
	return nil
}

func ensureFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("path is a symlink: %s", path)
	}
	if info.IsDir() {
		return fmt.Errorf("path is a directory: %s", path)
	}
	if info.Mode().Perm() != filePermission {
		if err := os.Chmod(path, filePermission); err != nil {
			return err
		}
	}
	return nil
}

func withMetadataLock(flags int, fn func() error) error {
	root, err := configDir()
	if err != nil {
		return err
	}
	if err := ensureDirectory(root); err != nil {
		return err
	}
	lockPath := filepath.Join(root, accountLockName)
	if err := ensureFile(lockPath); err != nil {
		return err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, filePermission)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := unix.Flock(int(f.Fd()), flags); err != nil {
		return err
	}
	defer unix.Flock(int(f.Fd()), unix.LOCK_UN)
	return fn()
}

func writeJSONAtomic(path string, payload []byte) error {
	if err := ensureDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	if err := ensureFile(path); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".nosnitch-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(filePermission); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	return nil
}

func loadAccounts() ([]Account, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}
	if err := ensureFile(path); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return []Account{}, nil
	}
	if err != nil {
		return nil, err
	}
	var accounts []Account
	if err := json.Unmarshal(b, &accounts); err != nil {
		return nil, err
	}
	return accounts, nil
}

func mergeAccount(existing, next Account) Account {
	if next.Provider == "" {
		next.Provider = existing.Provider
	}
	if next.Email == "" {
		next.Email = existing.Email
	}
	if next.Organization == "" {
		next.Organization = existing.Organization
	}
	if next.Alias == "" {
		next.Alias = existing.Alias
	}
	if next.Subject == "" {
		next.Subject = existing.Subject
	}
	if next.AuthKind == "" {
		next.AuthKind = existing.AuthKind
	}
	if next.LastStatus == "" {
		next.LastStatus = existing.LastStatus
	}
	if next.UpdatedAt.IsZero() {
		next.UpdatedAt = existing.UpdatedAt
	}
	if next.LastCheckedAt.IsZero() {
		next.LastCheckedAt = existing.LastCheckedAt
	}
	return next
}

func Load() ([]Account, error) {
	var accounts []Account
	if err := withMetadataLock(unix.LOCK_SH, func() error {
		loaded, err := loadAccounts()
		if err != nil {
			return err
		}
		accounts = loaded
		return nil
	}); err != nil {
		return nil, err
	}
	return accounts, nil
}

func Save(accounts []Account) error {
	return withMetadataLock(unix.LOCK_EX, func() error {
		path, err := configPath()
		if err != nil {
			return err
		}
		payload, err := json.MarshalIndent(accounts, "", "  ")
		if err != nil {
			return err
		}
		if err := writeJSONAtomic(path, append(payload, '\n')); err != nil {
			return err
		}
		return nil
	})
}

func Upsert(a Account) error {
	return withMetadataLock(unix.LOCK_EX, func() error {
		accounts, err := loadAccounts()
		if err != nil {
			return err
		}
		for i := range accounts {
			if accounts[i].ID != a.ID {
				continue
			}
			accounts[i] = mergeAccount(accounts[i], a)
			payload, err := json.MarshalIndent(accounts, "", "  ")
			if err != nil {
				return err
			}
			path, err := configPath()
			if err != nil {
				return err
			}
			return writeJSONAtomic(path, append(payload, '\n'))
		}
		accounts = append(accounts, a)
		payload, err := json.MarshalIndent(accounts, "", "  ")
		if err != nil {
			return err
		}
		path, err := configPath()
		if err != nil {
			return err
		}
		return writeJSONAtomic(path, append(payload, '\n'))
	})
}

func Remove(id string) error {
	return withMetadataLock(unix.LOCK_EX, func() error {
		accounts, err := loadAccounts()
		if err != nil {
			return err
		}
		kept := make([]Account, 0, len(accounts))
		found := false
		for _, account := range accounts {
			if account.ID == id {
				found = true
				continue
			}
			kept = append(kept, account)
		}
		if !found {
			return fmt.Errorf("account %q not found", id)
		}
		path, err := configPath()
		if err != nil {
			return err
		}
		payload, err := json.MarshalIndent(kept, "", "  ")
		if err != nil {
			return err
		}
		if err := writeJSONAtomic(path, append(payload, '\n')); err != nil {
			return err
		}

		credPath := CredentialPath(id)
		if credPath == "" {
			return fmt.Errorf("invalid credential path for id")
		}
		if err := ensureFile(credPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err := os.Remove(credPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	})
}

func SaveCredential(id string, c Credential) error {
	root, err := cacheDir()
	if err != nil {
		return err
	}
	if err := ensureDirectory(root); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	path := CredentialPath(id)
	if path == "" {
		return fmt.Errorf("invalid credential path")
	}
	if err := ensureFile(path); err != nil {
		return err
	}
	return writeJSONAtomic(path, append(payload, '\n'))
}

func LoadCredential(id string) (Credential, error) {
	var c Credential
	path := CredentialPath(id)
	if path == "" {
		return c, fmt.Errorf("invalid credential path")
	}
	if err := ensureFile(path); err != nil {
		return c, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	return c, nil
}

func Register(a Account, c Credential) error {
	if err := SaveCredential(a.ID, c); err != nil {
		return err
	}
	return withMetadataLock(unix.LOCK_EX, func() error {
		accounts, err := loadAccounts()
		if err != nil {
			return err
		}
		for i := range accounts {
			if accounts[i].ID != a.ID {
				continue
			}
			accounts[i] = mergeAccount(accounts[i], a)
			payload, err := json.MarshalIndent(accounts, "", "  ")
			if err != nil {
				return err
			}
			path, err := configPath()
			if err != nil {
				return err
			}
			return writeJSONAtomic(path, append(payload, '\n'))
		}
		accounts = append(accounts, a)
		payload, err := json.MarshalIndent(accounts, "", "  ")
		if err != nil {
			return err
		}
		path, err := configPath()
		if err != nil {
			return err
		}
		return writeJSONAtomic(path, append(payload, '\n'))
	})
}

// UpdateStatus records the latest read result while preserving account
// metadata and any fields written by another caller.
func UpdateStatus(id, status string, checkedAt time.Time) error {
	return withMetadataLock(unix.LOCK_EX, func() error {
		accounts, err := loadAccounts()
		if err != nil {
			return err
		}
		for i := range accounts {
			if accounts[i].ID != id {
				continue
			}
			accounts[i].LastStatus = status
			accounts[i].LastCheckedAt = checkedAt
			path, err := configPath()
			if err != nil {
				return err
			}
			payload, err := json.MarshalIndent(accounts, "", "  ")
			if err != nil {
				return err
			}
			return writeJSONAtomic(path, append(payload, '\n'))
		}
		return fmt.Errorf("account %q not found", id)
	})
}

// Package registry stores explicitly registered provider accounts and their
// short-lived credentials. Account metadata is configuration; credentials are
// kept separately so listing accounts never reads or prints secrets.
package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Account struct {
	ID           string    `json:"id"`
	Provider     string    `json:"provider"`
	Email        string    `json:"email,omitempty"`
	Organization string    `json:"organization,omitempty"`
	Alias        string    `json:"alias,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func configPath() string {
	root := os.Getenv("XDG_CONFIG_HOME")
	if root == "" {
		root = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(root, "nosnitch", "accounts.json")
}
func credentialPath(id string) string {
	root := os.Getenv("XDG_CACHE_HOME")
	if root == "" {
		root = filepath.Join(os.Getenv("HOME"), ".cache")
	}
	return filepath.Join(root, "nosnitch", "credentials", id+".json")
}

func Load() ([]Account, error) {
	b, e := os.ReadFile(configPath())
	if os.IsNotExist(e) {
		return []Account{}, nil
	}
	if e != nil {
		return nil, e
	}
	var a []Account
	if e = json.Unmarshal(b, &a); e != nil {
		return nil, e
	}
	return a, nil
}
func Save(a []Account) error {
	p := configPath()
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		return e
	}
	b, e := json.MarshalIndent(a, "", "  ")
	if e != nil {
		return e
	}
	return os.WriteFile(p, append(b, '\n'), 0600)
}
func Upsert(a Account) error {
	all, e := Load()
	if e != nil {
		return e
	}
	for i := range all {
		if all[i].ID == a.ID {
			all[i] = a
			return Save(all)
		}
	}
	all = append(all, a)
	return Save(all)
}
func Remove(id string) error {
	all, e := Load()
	if e != nil {
		return e
	}
	out := all[:0]
	found := false
	for _, a := range all {
		if a.ID == id {
			found = true
			continue
		}
		out = append(out, a)
	}
	if !found {
		return fmt.Errorf("account %q not found", id)
	}
	if e = Save(out); e != nil {
		return e
	}
	_ = os.Remove(credentialPath(id))
	return nil
}
func CredentialPath(id string) string { return credentialPath(id) }

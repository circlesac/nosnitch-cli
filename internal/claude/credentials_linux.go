//go:build linux

package claude

import (
	"fmt"
	"os"
	"path/filepath"
)

func oauthToken() (string, error) {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), ".claude")
	}
	path := filepath.Join(dir, ".credentials.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("Claude Code credentials not found: %s", path)
	}
	return parseOAuthToken(data)
}

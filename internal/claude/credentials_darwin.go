//go:build darwin

package claude

import (
	"fmt"
	"os/exec"
)

func oauthToken() (string, error) {
	out, err := exec.Command("security", "find-generic-password", "-w", "-s", "Claude Code-credentials").Output()
	if err != nil {
		return "", fmt.Errorf("Claude Code credentials not found in Keychain")
	}
	return parseOAuthToken(out)
}

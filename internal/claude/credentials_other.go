//go:build !darwin && !linux

package claude

import "fmt"

func oauthToken() (string, error) {
	return "", fmt.Errorf("Claude Code credentials are unsupported on this platform")
}

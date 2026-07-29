//go:build linux

package cookies

import (
	"crypto/sha1"
	"fmt"
	"os/exec"
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

func platformBrowsers() []Browser {
	return []Browser{
		{"Chrome", chromium, []string{".config/google-chrome/Default/Network/Cookies", ".config/google-chrome/Default/Cookies"}, []string{"chrome"}},
		{"Chromium", chromium, []string{".config/chromium/Default/Network/Cookies", ".config/chromium/Default/Cookies"}, []string{"chromium"}},
		{"Edge", chromium, []string{".config/microsoft-edge/Default/Network/Cookies", ".config/microsoft-edge/Default/Cookies"}, []string{"microsoft-edge", "chrome"}},
		{"Brave", chromium, []string{".config/BraveSoftware/Brave-Browser/Default/Network/Cookies", ".config/BraveSoftware/Brave-Browser/Default/Cookies"}, []string{"brave"}},
	}
}

func chromiumKey(browser Browser, scheme string) ([]byte, error) {
	if scheme == "v10" {
		return pbkdf2.Key([]byte("peanuts"), []byte("saltysalt"), 1, 16, sha1.New), nil
	}
	if scheme != "v11" {
		return nil, fmt.Errorf("unsupported %s cookie encryption for %s on Linux", scheme, browser.Name)
	}
	var lastErr error
	for _, application := range browser.secretServices {
		out, err := exec.Command("secret-tool", "lookup", "application", application).Output()
		if err != nil {
			lastErr = err
			continue
		}
		password := strings.TrimSuffix(string(out), "\n")
		if password != "" {
			return pbkdf2.Key([]byte(password), []byte("saltysalt"), 1, 16, sha1.New), nil
		}
	}
	if lastErr != nil {
		return nil, fmt.Errorf("read %s encryption password from Secret Service failed (install libsecret-tools and unlock the login keyring): %w", browser.Name, lastErr)
	}
	return nil, fmt.Errorf("%s encryption password not found in Secret Service", browser.Name)
}

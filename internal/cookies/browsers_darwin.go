//go:build darwin

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
		{"Chrome", chromium, []string{"Library/Application Support/Google/Chrome/Default/Network/Cookies", "Library/Application Support/Google/Chrome/Default/Cookies"}, []string{"Chrome Safe Storage"}},
		{"Edge", chromium, []string{"Library/Application Support/Microsoft Edge/Default/Network/Cookies", "Library/Application Support/Microsoft Edge/Default/Cookies"}, []string{"Microsoft Edge Safe Storage"}},
		{"Brave", chromium, []string{"Library/Application Support/BraveSoftware/Brave-Browser/Default/Network/Cookies", "Library/Application Support/BraveSoftware/Brave-Browser/Default/Cookies"}, []string{"Brave Safe Storage"}},
		{"Claude Desktop", chromium, []string{"Library/Application Support/Claude/Network/Cookies", "Library/Application Support/Claude/Cookies"}, []string{"Claude Safe Storage"}},
		{"Safari", safari, []string{"Library/Containers/com.apple.Safari/Data/Library/Cookies/Cookies.binarycookies"}, nil},
	}
}

func chromiumKey(browser Browser, scheme string) ([]byte, error) {
	if scheme != "v10" || len(browser.secretServices) == 0 {
		return nil, fmt.Errorf("unsupported %s cookie encryption for %s on macOS", scheme, browser.Name)
	}
	out, err := exec.Command("security", "find-generic-password", "-w", "-s", browser.secretServices[0]).Output()
	if err != nil {
		return nil, fmt.Errorf("read %q from Keychain failed (approve the prompt): %w", browser.secretServices[0], err)
	}
	return pbkdf2.Key([]byte(strings.TrimSuffix(string(out), "\n")), []byte("saltysalt"), 1003, 16, sha1.New), nil
}

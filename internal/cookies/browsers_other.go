//go:build !darwin && !linux

package cookies

import "fmt"

func platformBrowsers() []Browser { return nil }

func chromiumKey(browser Browser, scheme string) ([]byte, error) {
	return nil, fmt.Errorf("browser cookie encryption is unsupported on this platform")
}

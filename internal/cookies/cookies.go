// Package cookies reads and decrypts a browser's cookies for a host.
// Chromium cookies are read from their SQLite store and decrypted with the
// platform's browser credential store. Safari is supported on macOS.
package cookies

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

// ErrNeedFullDiskAccess means the cookie store exists but macOS blocked the read.
var ErrNeedFullDiskAccess = errors.New("full disk access required")

type kind int

const (
	chromium kind = iota
	safari
)

type Browser struct {
	Name           string
	kind           kind
	relPaths       []string // cookie store paths relative to $HOME, preferred first
	secretServices []string // platform credential-store lookup names
}

var known = platformBrowsers()

func (b Browser) path() string {
	for _, relPath := range b.relPaths {
		path := filepath.Join(os.Getenv("HOME"), filepath.FromSlash(relPath))
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	if len(b.relPaths) == 0 {
		return ""
	}
	return filepath.Join(os.Getenv("HOME"), filepath.FromSlash(b.relPaths[0]))
}

// Installed returns known browsers whose cookie store exists on this machine.
func Installed() []Browser {
	var out []Browser
	for _, b := range known {
		if _, err := os.Stat(b.path()); err == nil {
			out = append(out, b)
		}
	}
	return out
}

// Cookies returns decrypted cookies matching hostLike for this browser.
// Returns (nil, nil) when the browser has no matching session, and
// (nil, ErrNeedFullDiskAccess) when macOS blocks the read (Safari).
func (b Browser) Cookies(hostLike string) (map[string]string, error) {
	if b.kind == safari {
		return safariCookies(b.path(), hostLike)
	}
	return chromiumCookies(b, hostLike)
}

func (b Browser) ChatGPT() (map[string]string, error) { return b.Cookies("chatgpt.com") }

func (b Browser) Claude() (map[string]string, error) { return b.Cookies("claude.ai") }

func chromiumCookies(b Browser, hostLike string) (map[string]string, error) {
	db, cleanup, err := openCopy(b.path())
	if err != nil {
		return nil, err
	}
	defer cleanup()

	// Avoid a credential-store lookup when the browser has no matching session.
	var n int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM cookies WHERE host_key LIKE ?", "%"+hostLike).Scan(&n); err != nil || n == 0 {
		return nil, nil
	}

	var cookieDBVersion int
	var version string
	if db.QueryRow("SELECT value FROM meta WHERE key = 'version'").Scan(&version) == nil {
		cookieDBVersion, _ = strconv.Atoi(version)
	}
	rows, err := db.Query(
		"SELECT host_key, name, value, encrypted_value FROM cookies WHERE host_key LIKE ?", "%"+hostLike)
	if err != nil {
		return nil, fmt.Errorf("query cookies: %w", err)
	}
	defer rows.Close()

	jar := map[string]string{}
	keys := map[string][]byte{}
	keyFailures := map[string]error{}
	var keyFailure error
	for rows.Next() {
		var hostKey, name, value string
		var enc []byte
		if rows.Scan(&hostKey, &name, &value, &enc) != nil {
			continue
		}
		if value != "" {
			jar[name] = value
			continue
		}
		if len(enc) < 3 {
			continue
		}
		scheme := string(enc[:3])
		key, ok := keys[scheme]
		if !ok {
			if _, failed := keyFailures[scheme]; failed {
				continue
			}
			key, err = chromiumKey(b, scheme)
			if err != nil {
				keyFailures[scheme] = err
				if keyFailure == nil {
					keyFailure = err
				}
				continue
			}
			keys[scheme] = key
		}
		if value, ok := decryptChromium(enc, key, hostKey, cookieDBVersion >= 24); ok {
			jar[name] = value
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read cookies: %w", err)
	}
	if len(jar) == 0 {
		if keyFailure != nil {
			return nil, keyFailure
		}
		return nil, nil
	}
	return jar, nil
}

// openCopy copies the (locked) Cookies DB to a temp file and opens it read-only.
func openCopy(src string) (*sql.DB, func(), error) {
	in, err := os.Open(src)
	if err != nil {
		return nil, nil, err
	}
	defer in.Close()
	tmp, err := os.CreateTemp("", "nosnitch-cookies-*.db")
	if err != nil {
		return nil, nil, err
	}
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return nil, nil, err
	}
	tmp.Close()
	db, err := sql.Open("sqlite", tmp.Name())
	if err != nil {
		os.Remove(tmp.Name())
		return nil, nil, err
	}
	return db, func() { db.Close(); os.Remove(tmp.Name()) }, nil
}

func decryptChromium(enc, key []byte, hostKey string, hasHostDigest bool) (string, bool) {
	if len(enc) < 3+aes.BlockSize || (string(enc[:3]) != "v10" && string(enc[:3]) != "v11") {
		return "", false
	}
	ct := enc[3:]
	if len(ct)%aes.BlockSize != 0 {
		return "", false
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", false
	}
	iv := bytes.Repeat([]byte{0x20}, aes.BlockSize)
	pt := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(pt, ct)

	n := int(pt[len(pt)-1])
	if n < 1 || n > aes.BlockSize || n > len(pt) ||
		!bytes.Equal(pt[len(pt)-n:], bytes.Repeat([]byte{byte(n)}, n)) {
		return "", false
	}
	pt = pt[:len(pt)-n]
	if hasHostDigest {
		digest := sha256.Sum256([]byte(hostKey))
		if len(pt) < len(digest) || !bytes.Equal(pt[:len(digest)], digest[:]) {
			return "", false
		}
		pt = pt[len(digest):]
	}
	if !utf8.Valid(pt) {
		return "", false
	}
	return string(pt), true
}

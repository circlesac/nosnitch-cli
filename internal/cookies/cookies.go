// Package cookies reads and decrypts a browser's cookies for a host.
//
//	Chromium (Chrome/Edge/Brave): SQLite Cookies DB, values AES-128-CBC (v10)
//	    with a key from the macOS Keychain ("<Browser> Safe Storage").
//	Safari: Cookies.binarycookies (Apple binary format, plaintext values),
//	    but the file lives in a TCC-protected container → needs Full Disk Access.
package cookies

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/crypto/pbkdf2"
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
	Name        string
	kind        kind
	relPath     string // cookie store path relative to $HOME
	keychainSvc string // chromium: "<Browser> Safe Storage"
}

var known = []Browser{
	{"Chrome", chromium, "Library/Application Support/Google/Chrome/Default/Cookies", "Chrome Safe Storage"},
	{"Edge", chromium, "Library/Application Support/Microsoft Edge/Default/Cookies", "Microsoft Edge Safe Storage"},
	{"Brave", chromium, "Library/Application Support/BraveSoftware/Brave-Browser/Default/Cookies", "Brave Safe Storage"},
	{"Claude Desktop", chromium, "Library/Application Support/Claude/Cookies", "Claude Safe Storage"},
	{"Safari", safari, "Library/Containers/com.apple.Safari/Data/Library/Cookies/Cookies.binarycookies", ""},
}

func (b Browser) path() string { return filepath.Join(os.Getenv("HOME"), b.relPath) }

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

	// cheap plaintext pre-check — avoid a Keychain prompt when there's no session
	var n int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM cookies WHERE host_key LIKE ?", "%"+hostLike).Scan(&n); err != nil || n == 0 {
		return nil, nil
	}

	key, err := safeStorageKey(b.keychainSvc)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(
		"SELECT name, encrypted_value FROM cookies WHERE host_key LIKE ?", "%"+hostLike)
	if err != nil {
		return nil, fmt.Errorf("query cookies: %w", err)
	}
	defer rows.Close()

	jar := map[string]string{}
	for rows.Next() {
		var name string
		var enc []byte
		if rows.Scan(&name, &enc) != nil {
			continue
		}
		if v, ok := decryptV10(enc, key); ok {
			jar[name] = v
		}
	}
	if len(jar) == 0 {
		return nil, nil
	}
	return jar, nil
}

func safeStorageKey(service string) ([]byte, error) {
	out, err := exec.Command("security", "find-generic-password", "-w", "-s", service).Output()
	if err != nil {
		return nil, fmt.Errorf("read %q from Keychain failed (approve the prompt): %w", service, err)
	}
	pw := out
	if n := len(pw); n > 0 && pw[n-1] == '\n' {
		pw = pw[:n-1]
	}
	return pbkdf2.Key(pw, []byte("saltysalt"), 1003, 16, sha1.New), nil
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

func decryptV10(enc, key []byte) (string, bool) {
	if len(enc) < 3+aes.BlockSize || string(enc[:3]) != "v10" {
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
	iv := make([]byte, 16)
	for i := range iv {
		iv[i] = 0x20
	}
	pt := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(pt, ct)

	if n := int(pt[len(pt)-1]); n >= 1 && n <= aes.BlockSize && n <= len(pt) { // PKCS7 unpad
		pt = pt[:len(pt)-n]
	}
	if len(pt) >= 32 { // strip 32-byte SHA256(host_key) prefix (recent Chrome/macOS)
		pt = pt[32:]
	}
	for _, b := range pt {
		if b > 0x7f {
			return "", false
		}
	}
	return string(pt), true
}

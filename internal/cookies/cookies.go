// Package cookies reads and decrypts Chrome cookies for a given host (macOS, v1).
//
// Chrome on macOS encrypts cookie values with AES-128-CBC using a key derived
// (PBKDF2-HMAC-SHA1, salt "saltysalt", 1003 iters, 16 bytes) from the
// "Chrome Safe Storage" password in the login Keychain. Recent Chrome prepends
// a 32-byte SHA256(host_key) to the plaintext, which we strip.
package cookies

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
	"golang.org/x/crypto/pbkdf2"
)

// Chrome returns decrypted cookies whose host_key ends with hostLike
// (e.g. "chatgpt.com" matches both "chatgpt.com" and ".chatgpt.com").
func Chrome(hostLike string) (map[string]string, error) {
	dbPath := filepath.Join(os.Getenv("HOME"),
		"Library", "Application Support", "Google", "Chrome", "Default", "Cookies")
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("Chrome cookie DB not found (%s)", dbPath)
	}

	key, err := safeStorageKey()
	if err != nil {
		return nil, err
	}

	db, cleanup, err := openCopy(dbPath)
	if err != nil {
		return nil, err
	}
	defer cleanup()

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
		if err := rows.Scan(&name, &enc); err != nil {
			continue
		}
		if v, ok := decryptV10(enc, key); ok {
			jar[name] = v
		}
	}
	return jar, nil
}

func safeStorageKey() ([]byte, error) {
	out, err := exec.Command("security", "find-generic-password",
		"-w", "-s", "Chrome Safe Storage", "-a", "Chrome").Output()
	if err != nil {
		return nil, fmt.Errorf("read 'Chrome Safe Storage' from Keychain failed (approve the prompt): %w", err)
	}
	pw := strings.TrimSpace(string(out))
	return pbkdf2.Key([]byte(pw), []byte("saltysalt"), 1003, 16, sha1.New), nil
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

	// PKCS7 unpad
	if n := int(pt[len(pt)-1]); n >= 1 && n <= aes.BlockSize && n <= len(pt) {
		pt = pt[:len(pt)-n]
	}
	// strip 32-byte SHA256(host_key) prefix (recent Chrome on macOS)
	if len(pt) >= 32 {
		pt = pt[32:]
	}
	// cookie values are ASCII; reject anything that didn't decrypt cleanly
	for _, b := range pt {
		if b > 0x7f {
			return "", false
		}
	}
	return string(pt), true
}

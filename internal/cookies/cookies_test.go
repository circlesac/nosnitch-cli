package cookies

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"testing"
)

func TestDecryptChromium(t *testing.T) {
	key := []byte("0123456789abcdef")
	hostKey := ".chatgpt.com"
	digest := sha256.Sum256([]byte(hostKey))

	got, ok := decryptChromium(encryptCookie("v10", key, append(digest[:], []byte("session-value")...)), key, hostKey, true)
	if !ok || got != "session-value" {
		t.Fatalf("decryptChromium() = %q, %v", got, ok)
	}
}

func TestDecryptChromiumRejectsWrongHostDigest(t *testing.T) {
	key := []byte("0123456789abcdef")
	digest := sha256.Sum256([]byte(".claude.ai"))

	if got, ok := decryptChromium(encryptCookie("v11", key, append(digest[:], []byte("session-value")...)), key, ".chatgpt.com", true); ok {
		t.Fatalf("decryptChromium() = %q, true; want digest mismatch", got)
	}
}

func TestCookieDomainMatches(t *testing.T) {
	for domain, want := range map[string]bool{
		"github.com": true, ".github.com": true, "gist.github.com": false,
		"evilgithub.com": false, ".evilgithub.com": false,
	} {
		if got := cookieDomainMatches(domain, "github.com"); got != want {
			t.Fatalf("cookieDomainMatches(%q) = %v, want %v", domain, got, want)
		}
	}
}

func encryptCookie(scheme string, key, plaintext []byte) []byte {
	padding := aes.BlockSize - len(plaintext)%aes.BlockSize
	plaintext = append(plaintext, bytes.Repeat([]byte{byte(padding)}, padding)...)
	block, _ := aes.NewCipher(key)
	ciphertext := make([]byte, len(plaintext))
	cipher.NewCBCEncrypter(block, bytes.Repeat([]byte{0x20}, aes.BlockSize)).CryptBlocks(ciphertext, plaintext)
	return append([]byte(scheme), ciphertext...)
}

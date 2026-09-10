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
	"sort"
	"strconv"
	"strings"
	"time"
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
		expanded := expandProfiles(b)
		for _, candidate := range expanded {
			if _, err := os.Stat(candidate.path()); err == nil {
				out = append(out, candidate)
			}
		}
	}
	return out
}

// expandProfiles discovers Chromium profiles without assuming Default is the
// only account. Products whose cookie path is not profile-scoped are returned
// unchanged.
func expandProfiles(browser Browser) []Browser {
	marker := string(filepath.Separator) + "Default" + string(filepath.Separator)
	var root string
	for _, relPath := range browser.relPaths {
		if idx := strings.Index(relPath, marker); idx >= 0 {
			root = relPath[:idx]
			break
		}
	}
	if root == "" {
		return []Browser{browser}
	}
	profileRoot := filepath.Join(os.Getenv("HOME"), filepath.FromSlash(root))
	entries, err := os.ReadDir(profileRoot)
	if err != nil {
		return []Browser{browser}
	}
	var profiles []string
	for _, entry := range entries {
		if !entry.IsDir() || (entry.Name() != "Default" && !strings.HasPrefix(entry.Name(), "Profile ")) {
			continue
		}
		profiles = append(profiles, entry.Name())
	}
	if len(profiles) == 0 {
		return []Browser{browser}
	}
	sort.Strings(profiles)
	out := make([]Browser, 0, len(profiles))
	for _, profile := range profiles {
		candidate := browser
		candidate.relPaths = make([]string, 0, len(browser.relPaths))
		for _, relPath := range browser.relPaths {
			if idx := strings.Index(relPath, marker); idx >= 0 {
				candidate.relPaths = append(candidate.relPaths,
					filepath.ToSlash(filepath.Join(relPath[:idx], profile, relPath[idx+len(marker):])))
			} else {
				candidate.relPaths = append(candidate.relPaths, relPath)
			}
		}
		if profile != "Default" {
			candidate.Name = browser.Name + " (" + profile + ")"
		}
		out = append(out, candidate)
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

func (b Browser) GitHub() (map[string]string, error) { return b.Cookies("github.com") }

func chromiumCookies(b Browser, hostLike string) (map[string]string, error) {
	db, cleanup, err := openCopy(b.path())
	if err != nil {
		return nil, err
	}
	defer cleanup()

	// Avoid a credential-store lookup when the browser has no matching session.
	expiryColumn := hasColumn(db, "cookies", "expires_utc")
	where := "host_key = ? OR host_key = ?"
	args := []any{hostLike, "." + hostLike}
	if expiryColumn {
		where = "(" + where + ") AND (expires_utc = 0 OR expires_utc > ?)"
		args = append(args, chromiumNow())
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM cookies WHERE "+where, args...).Scan(&n); err != nil || n == 0 {
		return nil, nil
	}

	var cookieDBVersion int
	var version string
	if db.QueryRow("SELECT value FROM meta WHERE key = 'version'").Scan(&version) == nil {
		cookieDBVersion, _ = strconv.Atoi(version)
	}
	columns := "host_key, name, value, encrypted_value"
	if expiryColumn {
		columns = "host_key, name, value, encrypted_value, expires_utc"
	}
	rows, err := db.Query("SELECT "+columns+" FROM cookies WHERE "+where, args...)
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
		var expires int64
		if expiryColumn {
			if rows.Scan(&hostKey, &name, &value, &enc, &expires) != nil {
				continue
			}
		} else if rows.Scan(&hostKey, &name, &value, &enc) != nil {
			continue
		}
		if expiryColumn && expires != 0 && expires <= chromiumNow() {
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
	for _, suffix := range []string{"-wal", "-shm"} {
		sidecar, err := os.Open(src + suffix)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			os.Remove(tmp.Name())
			return nil, nil, err
		}
		sidecarPath := tmp.Name() + suffix
		sidecarOut, err := os.Create(sidecarPath)
		if err != nil {
			sidecar.Close()
			os.Remove(tmp.Name())
			return nil, nil, err
		}
		_, copyErr := io.Copy(sidecarOut, sidecar)
		closeErr := sidecarOut.Close()
		sidecar.Close()
		if copyErr != nil || closeErr != nil {
			os.Remove(tmp.Name())
			os.Remove(sidecarPath)
			if copyErr != nil {
				return nil, nil, copyErr
			}
			return nil, nil, closeErr
		}
	}
	db, err := sql.Open("sqlite", tmp.Name())
	if err != nil {
		os.Remove(tmp.Name())
		return nil, nil, err
	}
	return db, func() {
		db.Close()
		os.Remove(tmp.Name())
		os.Remove(tmp.Name() + "-wal")
		os.Remove(tmp.Name() + "-shm")
	}, nil
}

func chromiumNow() int64 {
	return (time.Now().UnixNano()/1000 + 11644473600000000)
}

func hasColumn(db *sql.DB, table, column string) bool {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, primaryKey int
		var defaultValue any
		if rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &primaryKey) == nil && name == column {
			return true
		}
	}
	return false
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

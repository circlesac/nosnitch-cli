package cookies

import (
	"encoding/binary"
	"errors"
	"os"
	"strings"
)

// safariChatGPT parses Safari's Cookies.binarycookies and returns chatgpt.com
// cookies. Values are plaintext (no Keychain), but the file is TCC-protected —
// a blocked read surfaces as ErrNeedFullDiskAccess.
func safariChatGPT(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrPermission) ||
			strings.Contains(strings.ToLower(err.Error()), "operation not permitted") {
			return nil, ErrNeedFullDiskAccess
		}
		return nil, err
	}
	jar := map[string]string{}
	for _, ck := range parseBinaryCookies(data) {
		if strings.Contains(ck.domain, "chatgpt.com") {
			jar[ck.name] = ck.value
		}
	}
	if len(jar) == 0 {
		return nil, nil
	}
	return jar, nil
}

type bcCookie struct{ domain, name, value string }

// parseBinaryCookies decodes Apple's Cookies.binarycookies format.
//
//	"cook" | pageCount(BE) | pageSizes[pageCount](BE) | pages…
//	page:   0x00000100 | cookieCount(LE) | offsets[count](LE) | 0 | cookies…
//	cookie: size(LE) | _ | flags(LE) | _ | urlOff nameOff pathOff valOff (LE) …
//	        then NUL-terminated strings at those offsets (relative to cookie start)
func parseBinaryCookies(b []byte) []bcCookie {
	if len(b) < 8 || string(b[:4]) != "cook" {
		return nil
	}
	nPages := binary.BigEndian.Uint32(b[4:8])
	off := 8
	sizes := make([]uint32, 0, nPages)
	for i := uint32(0); i < nPages && off+4 <= len(b); i++ {
		sizes = append(sizes, binary.BigEndian.Uint32(b[off:off+4]))
		off += 4
	}
	var out []bcCookie
	for _, sz := range sizes {
		if off+int(sz) > len(b) {
			break
		}
		out = append(out, parsePage(b[off:off+int(sz)])...)
		off += int(sz)
	}
	return out
}

func parsePage(p []byte) []bcCookie {
	if len(p) < 8 {
		return nil
	}
	nc := binary.LittleEndian.Uint32(p[4:8])
	var out []bcCookie
	for i := uint32(0); i < nc; i++ {
		if 12+i*4 > uint32(len(p)) {
			break
		}
		co := binary.LittleEndian.Uint32(p[8+i*4 : 12+i*4])
		if int(co)+32 > len(p) {
			continue
		}
		c := p[co:]
		out = append(out, bcCookie{
			domain: cstr(c, binary.LittleEndian.Uint32(c[16:20])),
			name:   cstr(c, binary.LittleEndian.Uint32(c[20:24])),
			value:  cstr(c, binary.LittleEndian.Uint32(c[28:32])),
		})
	}
	return out
}

func cstr(b []byte, off uint32) string {
	if int(off) >= len(b) {
		return ""
	}
	s := b[off:]
	for i := 0; i < len(s); i++ {
		if s[i] == 0 {
			return string(s[:i])
		}
	}
	return string(s)
}

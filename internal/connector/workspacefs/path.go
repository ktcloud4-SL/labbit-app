package workspacefs

import (
	"strings"
	"unicode/utf8"
)

// CanonicalPath follows the HTTP/Connector path contract without normalization.
func CanonicalPath(p string, directory bool) bool {
	if p == "" {
		return directory
	}
	if !utf8.ValidString(p) || len(p) > 4096 || p[0] == '/' || strings.HasSuffix(p, "/") {
		return false
	}
	for i := 0; i < len(p); i++ {
		c := p[i]
		if c < 32 || c == 127 || c == '\\' || (c == '%' && i+2 < len(p) && hex(p[i+1]) && hex(p[i+2])) {
			return false
		}
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." || len(part) > 255 {
			return false
		}
	}
	return true
}
func hex(b byte) bool { return b >= '0' && b <= '9' || b >= 'A' && b <= 'F' || b >= 'a' && b <= 'f' }

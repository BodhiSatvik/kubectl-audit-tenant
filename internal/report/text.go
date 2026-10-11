package report

import (
	"regexp"
	"strings"
	"unicode"
)

// CSI sequences (ESC [ ... final byte) and OSC sequences (ESC ] ... BEL or ESC \).
var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)`)

// Sanitize makes server text safe for a terminal: ANSI escapes and control characters are
// removed, tabs become spaces, and newlines fold to "; ".
func Sanitize(s string) string {
	s = ansi.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r == '\n' }), "; ")
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// clip cuts s to limit runes, ending in ellipsis when it had to cut.
func clip(s string, limit int, ellipsis string) (string, bool) {
	runes := []rune(s)
	if len(runes) <= limit {
		return s, false
	}
	return string(runes[:limit-len([]rune(ellipsis))]) + ellipsis, true
}

package pii

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const maskPrefix = "****"

// MaskTail returns "****" followed by the last n runes of value. The prefix has
// a fixed width, so the output never reveals the input's length. The tail is
// shown only when n > 0 and the value has at least 2n runes, so at least as much
// stays hidden as is shown; otherwise the result is "****". An empty value gives
// "" and invalid UTF-8 gives "****".
func MaskTail(value string, n int) string {
	if value == "" {
		return ""
	}
	if !utf8.ValidString(value) {
		return maskPrefix
	}
	if n <= 0 {
		return maskPrefix
	}
	runes := []rune(value)
	if n > len(runes)/2 {
		return maskPrefix
	}
	return maskPrefix + string(runes[len(runes)-n:])
}

// MaskAccountNumber masks a bank account number to "****" plus its last four
// digits, ignoring whitespace and '-'.
func MaskAccountNumber(value string) string {
	return maskStripped(value, func(r rune) bool { return unicode.IsSpace(r) || r == '-' }, false)
}

// MaskPAN masks a PAN to "****" plus its last four characters, upper-cased,
// ignoring whitespace, '-', '.' and '/'.
func MaskPAN(value string) string {
	return maskStripped(value, isCompactSeparator, true)
}

func maskStripped(value string, drop func(rune) bool, upper bool) string {
	if value == "" {
		return ""
	}
	if !utf8.ValidString(value) {
		return maskPrefix
	}
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range value {
		if drop(r) {
			continue
		}
		if upper {
			r = unicode.ToUpper(r)
		}
		b.WriteRune(r)
	}
	return MaskTail(b.String(), 4)
}

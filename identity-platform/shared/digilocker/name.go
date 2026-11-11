package digilocker

import (
	"sort"
	"strings"
	"unicode"
)

// NormaliseName reduces a person's name to a comparable form: case-folded,
// every run of characters that are not letters, marks or digits turned into
// one space, and the tokens sorted, so "KUMAR, Ravi" and "ravi kumar"
// normalise the same. Initials are kept as tokens ("R Kumar" does not match
// "Ravi Kumar"): a display name must carry the whole Aadhaar name.
func NormaliseName(name string) string {
	tokens := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsDigit(r))
	})
	sort.Strings(tokens)
	return strings.Join(tokens, " ")
}

// NamesMatch reports whether two names normalise to the same non-empty form.
func NamesMatch(a, b string) bool {
	na := NormaliseName(a)
	return na != "" && na == NormaliseName(b)
}

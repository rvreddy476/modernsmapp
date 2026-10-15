package postgres

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Search history rules (T-15, 2026-10-14). Pure functions so the recording
// and popular-searches rules can be unit-tested without a database.

const (
	// MaxSearchHistory is how many history rows a user keeps; older rows are
	// trimmed on every write. It is also the read cap on GET /history.
	MaxSearchHistory = 20

	// MinHistoryQueryRunes: a query shorter than this (after normalising)
	// is not worth remembering — single characters are typos or the first
	// keystroke of a search.
	MinHistoryQueryRunes = 2

	// MaxHistoryQueryRunes caps what is stored. validateSearchQuery already
	// refuses anything over 500 bytes; history keeps the first 200 runes.
	MaxHistoryQueryRunes = 200

	// RefineWindowSeconds: an earlier entry that is a strict prefix of the
	// new query and was written this recently is the same search being
	// typed or refined ("ca" → "cat" → "cats"), and is folded into it.
	RefineWindowSeconds = 60
)

// NormaliseHistoryQuery returns the display form (control characters
// removed, whitespace trimmed and collapsed, capped at MaxHistoryQueryRunes)
// and the dedupe key (the display form lower-cased). ok=false means the
// query is too short to record.
func NormaliseHistoryQuery(raw string) (display, key string, ok bool) {
	if !utf8.ValidString(raw) {
		raw = strings.ToValidUTF8(raw, "")
	}
	cleaned := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, raw)
	display = strings.Join(strings.Fields(cleaned), " ")
	if utf8.RuneCountInString(display) > MaxHistoryQueryRunes {
		display = strings.TrimSpace(string([]rune(display)[:MaxHistoryQueryRunes]))
	}
	if utf8.RuneCountInString(display) < MinHistoryQueryRunes {
		return "", "", false
	}
	return display, strings.ToLower(display), true
}

var (
	// An address: something@something.tld.
	emailLike = regexp.MustCompile(`[^\s@]+@[^\s@]+\.[^\s@]+`)
	// A phone or account number: seven or more digits, each optionally
	// followed by one separator (+91 98765 43210, 040-1234567, 9876543210).
	phoneLike = regexp.MustCompile(`(?:\d[\s().+-]?){7,}`)
	// An Indian PAN: five letters, four digits, one letter.
	panLike = regexp.MustCompile(`(?i)\b[a-z]{5}\d{4}[a-z]\b`)
)

// IsSafePopularQuery reports whether a query may be shown to other people
// as a popular search. Anything that looks like contact or identity data is
// held back even when it clears the distinct-user threshold.
func IsSafePopularQuery(q string) bool {
	q = strings.TrimSpace(q)
	if utf8.RuneCountInString(q) < MinHistoryQueryRunes {
		return false
	}
	if emailLike.MatchString(q) || phoneLike.MatchString(q) || panLike.MatchString(q) {
		return false
	}
	return true
}

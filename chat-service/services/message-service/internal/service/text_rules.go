package service

import (
	"regexp"
	"strings"
	"unicode"
)

// graphemeCount approximates user-perceived characters without a Unicode
// segmentation dependency: combining marks, variation selectors, emoji
// skin-tone modifiers, tag characters and anything joined by ZWJ extend the
// previous character, and two regional indicators make one flag.
func graphemeCount(s string) int {
	count := 0
	joinNext := false
	riOpen := false
	for _, r := range s {
		switch {
		case r == 0x200D: // zero-width joiner
			joinNext = true
			continue
		case unicode.Is(unicode.Mn, r), unicode.Is(unicode.Me, r), unicode.Is(unicode.Mc, r),
			r >= 0xFE00 && r <= 0xFE0F,   // variation selectors
			r >= 0x1F3FB && r <= 0x1F3FF, // skin tones
			r >= 0xE0020 && r <= 0xE007F: // tag characters
			continue
		case r >= 0x1F1E6 && r <= 0x1F1FF: // regional indicator
			if riOpen {
				riOpen = false
				continue
			}
			riOpen = true
		default:
			riOpen = false
		}
		if joinNext {
			joinNext = false
			continue
		}
		count++
	}
	return count
}

// ValidReactionEmoji is one grapheme, at most 16 bytes, not blank (§2.5).
func ValidReactionEmoji(e string) bool {
	if e == "" || len(e) > 16 || strings.TrimSpace(e) != e {
		return false
	}
	for _, r := range e {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return graphemeCount(e) == 1
}

// Layer-1 text rules for Notes (§5). A port of the dating-service layer-1
// chat-safety patterns (Architecture/services/dating-service/internal/
// moderation/layer1.go) — that matcher lives in another module and runs in
// shadow mode for dating chats; a Note is shown to many people, so here a
// hit refuses the note instead of only logging it.
var (
	notePhoneRe = regexp.MustCompile(`\+?\d[\d\s\-\(\)]{8,}\d`)
	noteEmailRe = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)
	// Placeholder vocabulary shared with dating layer 1; trust-safety owns
	// the curated list.
	noteAbuseWords = []string{"slut", "whore", "rape"}
	noteMoneyWords = []string{"send me money", "transfer money", "wire money", "paisa bhej", "rupay bhej", "paise bhejo"}
)

// noteTextAllowed applies the layer-1 rules to a Note's text.
func noteTextAllowed(text string) bool {
	if containsLink(text) || notePhoneRe.MatchString(text) || noteEmailRe.MatchString(text) {
		return false
	}
	lower := strings.ToLower(text)
	for _, w := range append(append([]string{}, noteAbuseWords...), noteMoneyWords...) {
		if strings.Contains(lower, w) {
			return false
		}
	}
	return true
}

// matchesKeyword reports whether text contains any of the recipient's
// filtered words (already lower-cased by the projection).
func matchesKeyword(text string, keywords []string) bool {
	if len(keywords) == 0 || text == "" {
		return false
	}
	lower := strings.ToLower(text)
	for _, k := range keywords {
		if k != "" && strings.Contains(lower, k) {
			return true
		}
	}
	return false
}

package moderation

import (
	"regexp"
	"sort"
	"strings"
)

// Unkind wording (Pulse mechanic M13, the kind-message check and the spark
// comment filter). A small, deliberately conservative list of insults,
// slurs and sexual pressure in English and romanised Hindi, matched as
// whole words. It nudges and hides; it never blocks or punishes — a false
// positive costs a "send anyway" tap, so the list stays short and plain.
// The LLM layer (layer2.go), when configured, catches what words cannot.

// UnkindCategory names why a text was flagged.
const (
	UnkindInsult = "insult"
	UnkindSlur   = "slur"
	UnkindSexual = "sexual_pressure"
)

var unkindLexicon = map[string][]string{
	UnkindInsult: {
		"idiot", "stupid", "moron", "loser", "ugly", "fatso", "pathetic", "disgusting", "worthless",
		"shut up", "kill yourself", "kys",
		"bewakoof", "pagal", "kamina", "kamini", "kutta", "kutti", "ullu", "gadha", "harami", "chutiya", "gandu",
	},
	UnkindSlur: {
		"bitch", "whore", "slut", "bastard", "randi", "chakka", "hijra",
		"madarchod", "behenchod", "bhenchod", "bhosdike", "bhosdi",
	},
	UnkindSexual: {
		"send nudes", "send nude", "nudes", "show me your body", "sexy pics", "send pics",
	},
}

var unkindPatterns = func() map[string]*regexp.Regexp {
	out := map[string]*regexp.Regexp{}
	for cat, words := range unkindLexicon {
		quoted := make([]string, 0, len(words))
		for _, w := range words {
			quoted = append(quoted, regexp.QuoteMeta(w))
		}
		out[cat] = regexp.MustCompile(`(?i)(^|[^\pL\pN])(` + strings.Join(quoted, "|") + `)($|[^\pL\pN])`)
	}
	return out
}()

// Unkind reports whether text uses unkind wording, and which categories.
func Unkind(text string) (bool, []string) {
	if strings.TrimSpace(text) == "" {
		return false, nil
	}
	var cats []string
	for cat, re := range unkindPatterns {
		if re.MatchString(text) {
			cats = append(cats, cat)
		}
	}
	sort.Strings(cats)
	return len(cats) > 0, cats
}

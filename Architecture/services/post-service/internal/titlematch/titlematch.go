// Package titlematch finds a curated film or TV title in a video's title,
// description and hashtags (copyright quick wins, founder 2026-10-24).
//
// The rule is deliberately narrow, because a hit holds a video from every
// viewer until a person reviews it:
//
//   - case- and accent-insensitive: "JAILER", "Jailer" and "Jäiler" are one
//     word; accents are folded only on Latin, Greek and Cyrillic letters, so
//     an Indic vowel sign is never stripped off its consonant;
//   - whole words only: "jailerbird" does not contain "jailer";
//   - a multi-word title matches as a contiguous run of words, whatever
//     separates them ("Jailer 2", "jailer-2", "JAILER_2", "#jailer_2"), or
//     written as one word ("jailer2", "#Jailer2");
//   - apostrophes are dropped rather than split on ("Ocean's" = "Oceans");
//   - a hashtag is its own field: "#jailer" matches "Jailer".
//
// A catalogue entry is its title plus its aliases; any of them is a hit.
// The matcher is built once per catalogue snapshot and is safe for
// concurrent use.
package titlematch

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"
)

// Fields a match can be found in, in the order they are searched.
const (
	FieldTitle       = "title"
	FieldDescription = "description"
	FieldHashtag     = "hashtag"
)

// MinTermRunes is the shortest term (after normalisation, separators
// removed) the catalogue accepts. Two keeps titles like "96" while refusing
// a one-letter title that would hold every video mentioning it.
const MinTermRunes = 2

// Entry is one catalogue title.
type Entry struct {
	ID      uuid.UUID
	Title   string
	Aliases []string
}

// Match is one hit: which entry, which of its terms, in which field.
type Match struct {
	EntryID uuid.UUID
	Title   string // the entry's title (canonical)
	Term    string // the title or alias that hit, as staff wrote it
	Field   string
}

type phrase struct {
	entry  int
	term   string
	tokens []string
	joined string
}

// Matcher answers whether a text carries any catalogue term.
type Matcher struct {
	entries []Entry
	// byFirst indexes phrases by their first token; byJoined by the
	// concatenation of their tokens (the "written as one word" form, which
	// for a one-word phrase is the word itself).
	byFirst  map[string][]int
	byJoined map[string][]int
	phrases  []phrase
}

// New builds a matcher. Terms that normalise to nothing (or shorter than
// MinTermRunes) are skipped; ValidTerm is what the catalogue's writer
// checks first so none ever is.
func New(entries []Entry) *Matcher {
	m := &Matcher{
		entries:  append([]Entry(nil), entries...),
		byFirst:  map[string][]int{},
		byJoined: map[string][]int{},
	}
	for i, e := range m.entries {
		seen := map[string]bool{}
		for _, term := range append([]string{e.Title}, e.Aliases...) {
			toks := Tokens(term)
			joined := strings.Join(toks, "")
			if len(toks) == 0 || utf8.RuneCountInString(joined) < MinTermRunes {
				continue
			}
			key := strings.Join(toks, " ")
			if seen[key] {
				continue
			}
			seen[key] = true
			idx := len(m.phrases)
			m.phrases = append(m.phrases, phrase{entry: i, term: term, tokens: toks, joined: joined})
			m.byFirst[toks[0]] = append(m.byFirst[toks[0]], idx)
			if len(toks) > 1 {
				m.byJoined[joined] = append(m.byJoined[joined], idx)
			}
		}
	}
	return m
}

// Len is the number of searchable terms.
func (m *Matcher) Len() int {
	if m == nil {
		return 0
	}
	return len(m.phrases)
}

// ValidTerm reports whether a catalogue term is searchable at all.
func ValidTerm(term string) bool {
	toks := Tokens(term)
	return len(toks) > 0 && utf8.RuneCountInString(strings.Join(toks, "")) >= MinTermRunes
}

// MatchAll returns every entry hit by the title, the description or a
// hashtag: at most one Match per entry (the first field, then the first
// term, it was found by), in field order. nil when nothing matches.
func (m *Matcher) MatchAll(title, description string, hashtags []string) []Match {
	if m == nil || len(m.phrases) == 0 {
		return nil
	}
	var out []Match
	hit := map[int]bool{}
	add := func(field string, idxs []int) {
		for _, idx := range idxs {
			p := m.phrases[idx]
			if hit[p.entry] {
				continue
			}
			hit[p.entry] = true
			e := m.entries[p.entry]
			out = append(out, Match{EntryID: e.ID, Title: e.Title, Term: p.term, Field: field})
		}
	}
	add(FieldTitle, m.find(Tokens(title)))
	add(FieldDescription, m.find(Tokens(description)))
	for _, tag := range hashtags {
		add(FieldHashtag, m.find(Tokens(tag)))
	}
	return out
}

// find returns the phrases present in one token sequence, in order of
// first appearance.
func (m *Matcher) find(toks []string) []int {
	var out []int
	seen := map[int]bool{}
	for i, t := range toks {
		for _, idx := range m.byFirst[t] {
			if seen[idx] {
				continue
			}
			if hasRunAt(toks, i, m.phrases[idx].tokens) {
				seen[idx] = true
				out = append(out, idx)
			}
		}
		for _, idx := range m.byJoined[t] {
			if !seen[idx] {
				seen[idx] = true
				out = append(out, idx)
			}
		}
	}
	// A whole hashtag written as several tokens ("#jailer_2") is caught by
	// the run above; one written as one word ("#jailer2") by byJoined.
	return out
}

func hasRunAt(toks []string, at int, want []string) bool {
	if at+len(want) > len(toks) {
		return false
	}
	for j, w := range want {
		if toks[at+j] != w {
			return false
		}
	}
	return true
}

// foldable are the scripts whose combining marks are accents (dropped):
// "é" is "e". In Devanagari, Tamil and the other Indic scripts a combining
// mark is a vowel sign or virama, part of the letter, and is kept.
var foldable = []*unicode.RangeTable{unicode.Latin, unicode.Greek, unicode.Cyrillic}

// letterFolds are the Latin letters NFKD does not decompose.
var letterFolds = map[rune]string{
	'ß': "ss", 'æ': "ae", 'œ': "oe", 'ø': "o", 'đ': "d", 'ł': "l", 'ı': "i", 'þ': "th", 'ð': "d",
}

// isApostrophe: dropped, not split on ("ocean's" → "oceans").
func isApostrophe(r rune) bool {
	switch r {
	case '\'', '’', '‘', 'ʼ', '`', '´':
		return true
	}
	return false
}

// Tokens normalises s and splits it into words: compatibility-decomposed,
// lower-cased, accents folded on Latin, Greek and Cyrillic, apostrophes and
// zero-width joiners removed, and every other non-letter, non-digit,
// non-mark rune treated as a separator.
func Tokens(s string) []string {
	if s == "" {
		return nil
	}
	decomposed := norm.NFKD.String(s)
	var (
		out      []string
		cur      strings.Builder
		prevBase rune = -1
	)
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, norm.NFC.String(cur.String()))
			cur.Reset()
		}
	}
	for _, r := range decomposed {
		switch {
		case unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r):
			if prevBase >= 0 && unicode.In(prevBase, foldable...) {
				continue // an accent on a Latin/Greek/Cyrillic letter
			}
			if cur.Len() > 0 {
				cur.WriteRune(r) // an Indic vowel sign, virama, nukta…
			}
		case unicode.Is(unicode.Mc, r):
			if cur.Len() > 0 {
				cur.WriteRune(r)
			}
		case isApostrophe(r), r == '‍', r == '‌', r == '­':
			// dropped
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			lr := unicode.ToLower(r)
			if f, ok := letterFolds[lr]; ok {
				cur.WriteString(f)
			} else {
				cur.WriteRune(lr)
			}
			prevBase = r
		default:
			flush()
			prevBase = -1
		}
	}
	flush()
	return mergeInitials(out)
}

// mergeInitials joins a run of two or more one-letter words into one, so a
// dotted or spaced acronym reads the same however it is written: "K.G.F",
// "K G F" and "KGF" are all "kgf". Digits are not merged ("2 3" stays two
// words).
func mergeInitials(toks []string) []string {
	initial := func(t string) bool {
		r, n := utf8.DecodeRuneInString(t)
		return n == len(t) && unicode.IsLetter(r)
	}
	out := toks[:0:0]
	for i := 0; i < len(toks); {
		j := i
		for j < len(toks) && initial(toks[j]) {
			j++
		}
		if j-i >= 2 {
			out = append(out, strings.Join(toks[i:j], ""))
			i = j
			continue
		}
		out = append(out, toks[i])
		i++
	}
	return out
}

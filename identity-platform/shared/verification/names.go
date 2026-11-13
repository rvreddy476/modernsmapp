package verification

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Impersonation keys (docs/designs/short-posts-design.md section 3.4, "held
// at sign-up and at every name change").
//
// A name is reduced to a skeleton: compatibility-decomposed (fullwidth and
// mathematical letters become plain ones), accents on Latin letters dropped,
// lower-cased, look-alike letters from Cyrillic and Greek and look-alike
// digits and symbols folded onto one Latin letter, everything that is not a
// letter or digit removed, "rn" read as "m" and "vv" as "w", and runs of one
// letter collapsed. Two names with the same skeleton look the same to a
// reader.
//
// A reserved name (an Official or Government name, or a verified person's
// display name or handle) is stored as its skeletons, in written and in
// sorted word order. A candidate name additionally loses decorations an
// impersonator adds ("official", "real", "the", "hq", "india", ...), on the
// candidate side only: "NASA Official" is held against NASA, but "Air" is
// not held against "Air India".
//
// "Closely matches" for Official and Government names is edit distance one
// on the skeleton, found with deletion neighbourhoods (FuzzyVariants) so the
// store can answer it with one indexed lookup. A verified PERSON's name is
// matched on the exact skeleton only: person names are shared by many real
// people, and one edit of "Rahul Sharma" is another real person's name.

// MinKeyLen is the shortest skeleton (in runes) that is reserved or checked.
// Shorter names ("AB") would hold far more innocent names than impostors.
const MinKeyLen = 3

// MinFuzzyLen is the shortest skeleton that also matches at edit distance
// one. Below it ("bbc" vs "bbs") one edit is a different, ordinary word.
const MinFuzzyLen = 5

// decorationWords are what an impersonator adds around a real name. Folded
// through the skeleton at init so they compare with folded tokens.
var decorationWords = []string{
	"official", "offical", "officiel", "real", "the", "hq", "team", "verified",
	"original", "page", "account", "support", "india", "in", "global", "tv",
	"ltd", "limited", "pvt", "private", "inc", "llp", "corp", "company", "co",
}

var (
	foldedDecorations  map[string]bool
	joinedDecorations  []string // decorations stripped from an unspaced name
	minJoinedRemainder = 3
)

func init() {
	foldedDecorations = map[string]bool{}
	for _, w := range decorationWords {
		f := skeleton(w)
		foldedDecorations[f] = true
		// "in" and "tv" are too short to strip from the inside of an
		// unspaced handle ("kevin" is not "kev" + "in").
		if utf8.RuneCountInString(f) >= 4 {
			joinedDecorations = append(joinedDecorations, f)
		}
	}
	sort.Slice(joinedDecorations, func(i, j int) bool { return len(joinedDecorations[i]) > len(joinedDecorations[j]) })
}

// homoglyphs folds look-alike characters onto one Latin letter. Applied
// after lower-casing.
var homoglyphs = map[rune]string{
	// Cyrillic
	'а': "a", 'в': "b", 'е': "e", 'ё': "e", 'к': "k", 'м': "m", 'н': "h", 'о': "o", 'р': "p",
	'с': "c", 'т': "t", 'у': "y", 'х': "x", 'і': "i", 'ї': "i", 'ј': "j", 'ѕ': "s", 'һ': "h",
	'ԁ': "d", 'ԛ': "q", 'ԝ': "w", 'ӏ': "l", 'ɡ': "g",
	// Greek
	'α': "a", 'β': "b", 'ε': "e", 'ι': "i", 'κ': "k", 'ν': "v", 'ο': "o", 'ρ': "p", 'τ': "t",
	'υ': "u", 'χ': "x", 'γ': "y", 'ω': "w", 'η': "n",
	// Latin letters NFKD does not decompose
	'ı': "i", 'ł': "l", 'ø': "o", 'đ': "d", 'ħ': "h", 'ß': "ss", 'æ': "ae", 'œ': "oe",
	// Digits and symbols read as letters. "i", "1", "l", "|" and "!" all
	// fold to "l": in many fonts they are one vertical stroke.
	'0': "o", '1': "l", 'i': "l", '|': "l", '!': "l", '3': "e", '4': "a", '5': "s",
	'7': "t", '8': "b", '@': "a", '$': "s",
}

// upperHomoglyphs are capitals whose lower case no longer looks Latin (Greek
// capital Nu is "N", its lower case is "v"). Checked before lower-casing.
var upperHomoglyphs = map[rune]string{
	'Α': "a", 'Β': "b", 'Ε': "e", 'Ζ': "z", 'Η': "h", 'Ι': "l", 'Κ': "k", 'Μ': "m", 'Ν': "n",
	'Ο': "o", 'Ρ': "p", 'Τ': "t", 'Υ': "y", 'Χ': "x",
	'А': "a", 'В': "b", 'Е': "e", 'К': "k", 'М': "m", 'Н': "h", 'О': "o", 'Р': "p", 'С': "c",
	'Т': "t", 'Х': "x", 'У': "y", 'Ѕ': "s", 'І': "l", 'Ј': "j",
}

// skeleton folds one string (no word handling).
func skeleton(s string) string {
	var b strings.Builder
	lastLatin := false
	for _, r := range norm.NFKD.String(s) {
		if unicode.Is(unicode.Mn, r) {
			if lastLatin {
				continue // an accent on a Latin letter
			}
			b.WriteRune(r)
			continue
		}
		if f, ok := upperHomoglyphs[r]; ok {
			b.WriteString(f)
			lastLatin = true
			continue
		}
		r = unicode.ToLower(r)
		if f, ok := homoglyphs[r]; ok {
			b.WriteString(f)
			lastLatin = true
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mc, r) {
			b.WriteRune(r)
			lastLatin = r < 0x250
		}
	}
	out := b.String()
	out = strings.ReplaceAll(out, "rn", "m")
	out = strings.ReplaceAll(out, "vv", "w")
	return collapseRuns(out)
}

func collapseRuns(s string) string {
	var b strings.Builder
	var prev rune = -1
	for _, r := range s {
		if r == prev {
			continue
		}
		b.WriteRune(r)
		prev = r
	}
	return b.String()
}

// words splits a name into folded words. Separators are anything that is
// neither a letter, a mark, a digit nor a look-alike symbol.
func words(name string) []string {
	raw := strings.FieldsFunc(name, func(r rune) bool {
		if _, ok := homoglyphs[unicode.ToLower(r)]; ok {
			return false
		}
		return !(unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsDigit(r))
	})
	out := make([]string, 0, len(raw))
	for _, w := range raw {
		if f := skeleton(w); f != "" {
			out = append(out, f)
		}
	}
	return out
}

func joinKey(ws []string) string { return skeleton(strings.Join(ws, "")) }

func sortedCopy(ws []string) []string {
	c := append([]string(nil), ws...)
	sort.Strings(c)
	return c
}

func addKey(set map[string]bool, k string) {
	if utf8.RuneCountInString(k) >= MinKeyLen {
		set[k] = true
	}
}

func keyList(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ReservedKeys are the skeletons a reserved name is stored under: written
// and sorted word order. Empty for a name shorter than MinKeyLen.
func ReservedKeys(name string) []string {
	ws := words(name)
	set := map[string]bool{}
	addKey(set, joinKey(ws))
	addKey(set, joinKey(sortedCopy(ws)))
	return keyList(set)
}

// CandidateKeys are the skeletons a new display name or handle is checked
// under: the reserved keys, plus the same with impersonation decorations
// removed (whole words, and long decorations at either end of an unspaced
// handle).
func CandidateKeys(name string) []string {
	ws := words(name)
	set := map[string]bool{}
	addKey(set, joinKey(ws))
	addKey(set, joinKey(sortedCopy(ws)))

	kept := make([]string, 0, len(ws))
	for _, w := range ws {
		if !foldedDecorations[w] {
			kept = append(kept, w)
		}
	}
	if len(kept) > 0 {
		addKey(set, joinKey(kept))
		addKey(set, joinKey(sortedCopy(kept)))
	}
	for _, k := range keyList(set) {
		addKey(set, stripJoinedDecorations(k))
	}
	return keyList(set)
}

// stripJoinedDecorations removes long decorations from the ends of an
// unspaced key until none is left, keeping at least minJoinedRemainder runes.
func stripJoinedDecorations(k string) string {
	for changed := true; changed; {
		changed = false
		for _, d := range joinedDecorations {
			if strings.HasSuffix(k, d) && utf8.RuneCountInString(k)-utf8.RuneCountInString(d) >= minJoinedRemainder {
				k, changed = strings.TrimSuffix(k, d), true
			}
			if strings.HasPrefix(k, d) && utf8.RuneCountInString(k)-utf8.RuneCountInString(d) >= minJoinedRemainder {
				k, changed = strings.TrimPrefix(k, d), true
			}
		}
	}
	return collapseRuns(k)
}

// FuzzyVariants is the deletion neighbourhood of key: key itself and, when
// it has at least MinFuzzyLen runes, every string with one rune removed. Two
// keys are within one substitution, insertion, deletion or adjacent swap of
// each other exactly when their neighbourhoods meet.
func FuzzyVariants(key string) []string {
	rs := []rune(key)
	set := map[string]bool{key: true}
	if len(rs) >= MinFuzzyLen {
		for i := range rs {
			v := string(rs[:i]) + string(rs[i+1:])
			set[v] = true
		}
	}
	return keyList(set)
}

// FuzzyVariantsOf is the union of FuzzyVariants over keys.
func FuzzyVariantsOf(keys []string) []string {
	set := map[string]bool{}
	for _, k := range keys {
		for _, v := range FuzzyVariants(k) {
			set[v] = true
		}
	}
	return keyList(set)
}

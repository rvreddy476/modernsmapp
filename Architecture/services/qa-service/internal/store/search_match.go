package store

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/atpost/qa-service/database"
)

// ── Indexed search (audit M6) ───────────────────────────────────────────────
//
// /search and /questions/similar used unanchored ILIKE '%q%' over every
// question's title and full body: a sequential scan per call, open to
// anonymous callers. They now match a full-text document through GIN
// indexes built CONCURRENTLY at boot (database.ConcurrentIndexes):
// idx_questions_search_tsv over title and body, idx_questions_title_tsv
// over the title. Each search word must match the start of a word in the
// question ("sal" finds "salary"), in any order.
//
// The search text is reduced to letters and digits and bounded in words and
// length (SearchTerms), so nothing the caller sends is ever parsed as
// tsquery syntax (PrefixTSQuery).

const (
	// maxSearchTerms bounds the words one query matches on.
	maxSearchTerms = 8
	// maxSearchTermRunes bounds one word.
	maxSearchTermRunes = 64
)

// SearchTerms splits q into lowercase words of letters and digits, without
// duplicates, at most maxSearchTerms of at most maxSearchTermRunes each.
func SearchTerms(q string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if utf8.RuneCountInString(w) > maxSearchTermRunes {
			w = string([]rune(w)[:maxSearchTermRunes])
		}
		if seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
		if len(out) == maxSearchTerms {
			break
		}
	}
	return out
}

// SearchText is the bounded search text: the words of q (SearchTerms)
// joined by spaces; "" when there is nothing to search for.
func SearchText(q string) string {
	return strings.Join(SearchTerms(q), " ")
}

// PrefixTSQuery is the to_tsquery text for q: every word of SearchTerms as
// a word prefix ("w:*"), all required; "" when there is nothing to search
// for. The words are letters and digits only, so the text carries no
// tsquery syntax beyond what this adds; to_tsquery then runs each word
// through the same parser as the indexed document (a word it splits, such
// as "1e5abc", becomes adjacent lexemes on both sides).
func PrefixTSQuery(q string) string {
	terms := SearchTerms(q)
	for i, t := range terms {
		terms[i] = t + ":*"
	}
	return strings.Join(terms, " & ")
}

// prefixQuerySQL is the tsquery for the PrefixTSQuery text bound at arg.
// to_tsquery(regconfig, text) is immutable, so with a bound parameter the
// predicate is a plain indexable "document @@ constant" (the earlier
// subquery form kept the planner off the GIN index in CI).
func prefixQuerySQL(arg string) string {
	return "to_tsquery('simple', " + arg + ")"
}

// searchMatchSQL is the indexed title-and-body predicate over alias for the
// tsquery text in arg (e.g. "$1").
func searchMatchSQL(alias, arg string) string {
	return fmt.Sprintf(database.SearchDocumentSQL, alias) + " @@ " + prefixQuerySQL(arg)
}

// titleMatchSQL is the indexed title predicate over alias.
func titleMatchSQL(alias, arg string) string {
	return fmt.Sprintf(database.TitleDocumentSQL, alias) + " @@ " + prefixQuerySQL(arg)
}

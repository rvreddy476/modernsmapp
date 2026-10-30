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
// The search text is reduced to letters and digits (SearchText) and bounded
// in words and length; the database splits it with the same parser as the
// document and quotes every lexeme, so nothing the caller sends is ever
// parsed as tsquery syntax.

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

// prefixQuerySQL turns the search text in arg into a tsquery that matches
// every word as a word prefix. The words are split by the same text search
// parser that built the indexed document (so "1e5abc" or "tok-42" split the
// same way on both sides), each lexeme is quoted (never parsed as tsquery
// syntax) and suffixed with :*, and at most maxSearchTerms are used.
func prefixQuerySQL(arg string) string {
	return `(SELECT string_agg(quote_literal(l.lexeme) || ':*', ' & ') FROM (SELECT lexeme FROM unnest(to_tsvector('simple', ` +
		arg + `)) LIMIT ` + fmt.Sprint(maxSearchTerms) + `) l)::tsquery`
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

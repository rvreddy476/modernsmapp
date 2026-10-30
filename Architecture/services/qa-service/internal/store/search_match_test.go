package store

import (
	"strings"
	"testing"
)

func TestSearchTextIsBoundedAndInert(t *testing.T) {
	cases := map[string]string{
		"How to  cook Rice?":          "how to cook rice",
		"":                            "",
		"   !!! ":                     "",
		"a & b | !c ) ( ' : * \\ <->": "a b c",
		"salary SALARY salary":        "salary",
		"tok-42 1e5abc":               "tok 42 1e5abc",
	}
	for in, want := range cases {
		if got := SearchText(in); got != want {
			t.Errorf("SearchText(%q) = %q, want %q", in, got, want)
		}
	}
	// Whatever the script, a term is letters and digits only.
	for _, in := range []string{"नमस्ते दुनिया", "日本語 テスト", "ça & co"} {
		for _, term := range SearchTerms(in) {
			if strings.ContainsAny(term, "&|!():*'\\<> ") {
				t.Errorf("SearchTerms(%q) produced %q", in, term)
			}
		}
	}
	long := strings.Repeat("word ", 50) + strings.Repeat("x", 500)
	terms := SearchTerms(long + " " + strings.Join(strings.Fields("a b c d e f g h i j k"), " "))
	if len(terms) > maxSearchTerms {
		t.Fatalf("%d terms", len(terms))
	}
	for _, term := range SearchTerms(strings.Repeat("y", 500)) {
		if len([]rune(term)) > maxSearchTermRunes {
			t.Fatalf("term of %d runes", len([]rune(term)))
		}
	}
}

func TestSearchPredicatesUseTheIndexedExpressions(t *testing.T) {
	if got := searchMatchSQL("q", "$1"); !strings.HasPrefix(got, "to_tsvector('simple', left(coalesce(q.title, '') || ' ' || coalesce(q.body, ''), 100000)) @@ (SELECT") {
		t.Fatalf("search predicate %q", got)
	}
	if got := titleMatchSQL("q", "$2"); !strings.HasPrefix(got, "to_tsvector('simple', coalesce(q.title, '')) @@ (SELECT") {
		t.Fatalf("title predicate %q", got)
	}
	// The caller's text only ever reaches to_tsvector as a bind parameter,
	// and every lexeme is quoted before it becomes a tsquery.
	q := prefixQuerySQL("$7")
	for _, want := range []string{"to_tsvector('simple', $7)", "quote_literal(l.lexeme) || ':*'", "LIMIT 8", ")::tsquery"} {
		if !strings.Contains(q, want) {
			t.Errorf("prefix query %q lacks %q", q, want)
		}
	}
}

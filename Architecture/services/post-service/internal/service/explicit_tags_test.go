package service

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// Explicit hashtags / mentions (2026-09-05): validation tables and the
// merge with what the caption parser extracts.

func TestNormalizeExplicitHashtags(t *testing.T) {
	long := strings.Repeat("a", 51)
	cases := []struct {
		name string
		in   []string
		want []string
		err  error
	}{
		{"nil", nil, nil, nil},
		{"empty", []string{}, nil, nil},
		{"plain", []string{"momentum", "test"}, []string{"momentum", "test"}, nil},
		{"leading # stripped, lowercased", []string{"#Momentum", "TEST"}, []string{"momentum", "test"}, nil},
		{"whitespace forgiven", []string{" #tag "}, []string{"tag"}, nil},
		{"dedupe after normalisation", []string{"Tag", "#tag", "TAG"}, []string{"tag"}, nil},
		{"unicode letters and digits", []string{"ಕನ್ನಡ", "日本語", "año2026"}, []string{"ಕನ್ನಡ", "日本語", "año2026"}, nil},
		{"underscore ok", []string{"snake_case"}, []string{"snake_case"}, nil},
		{"50 chars ok", []string{strings.Repeat("a", 50)}, []string{strings.Repeat("a", 50)}, nil},
		{"51 chars", []string{long}, nil, ErrInvalidHashtag},
		{"empty tag", []string{""}, nil, ErrInvalidHashtag},
		{"bare #", []string{"#"}, nil, ErrInvalidHashtag},
		{"space inside", []string{"two words"}, nil, ErrInvalidHashtag},
		{"hyphen", []string{"no-hyphens"}, nil, ErrInvalidHashtag},
		{"double #", []string{"##x"}, nil, ErrInvalidHashtag},
		{"31 tags", make([]string, 31), nil, ErrTooManyHashtags},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeExplicitHashtags(tc.in)
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Fatalf("want %v, got %v", tc.err, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
	// Exactly 30 is allowed.
	thirty := make([]string, 30)
	for i := range thirty {
		thirty[i] = strings.Repeat("t", i+1)
	}
	if got, err := NormalizeExplicitHashtags(thirty); err != nil || len(got) != 30 {
		t.Fatalf("30 tags must pass: got %d err %v", len(got), err)
	}
}

func TestNormalizeExplicitMentions(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
		err  error
	}{
		{"nil", nil, nil, nil},
		{"plain", []string{"call.usera", "call_b"}, []string{"call.usera", "call_b"}, nil},
		{"leading @ stripped, case kept", []string{"@Call.UserA"}, []string{"Call.UserA"}, nil},
		{"dedupe case-insensitively", []string{"alice", "@Alice"}, []string{"alice"}, nil},
		{"whitespace forgiven", []string{" @bob "}, []string{"bob"}, nil},
		{"empty", []string{""}, nil, ErrInvalidMention},
		{"bare @", []string{"@"}, nil, ErrInvalidMention},
		{"space inside", []string{"a b"}, nil, ErrInvalidMention},
		{"31 chars", []string{strings.Repeat("u", 31)}, nil, ErrInvalidMention},
		{"21 mentions", make([]string, 21), nil, ErrTooManyMentions},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeExplicitMentions(tc.in)
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Fatalf("want %v, got %v", tc.err, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

// Merge: caption-parsed first, explicit after, duplicates dropped, cap
// honoured — so hashtag pages and mention notifications behave as if the
// explicit tags had been in the text.
func TestMergeTagsDedupesAndKeepsOrder(t *testing.T) {
	parsed := extractHashtags("launch day #Momentum #reels")
	if !reflect.DeepEqual(parsed, []string{"momentum", "reels"}) {
		t.Fatalf("parser sanity: %v", parsed)
	}
	explicit, _ := NormalizeExplicitHashtags([]string{"#Momentum", "test", "reels", "new"})
	got := mergeTags(parsed, explicit, maxMergedHashtags)
	if want := []string{"momentum", "reels", "test", "new"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}

	// Mentions: the parser keeps case; the explicit list dedupes against
	// it case-insensitively.
	pm := extractMentions("hi @Alice and @bob")
	em, _ := NormalizeExplicitMentions([]string{"@alice", "carol"})
	if got := mergeTags(pm, em, maxMergedMentions); !reflect.DeepEqual(got, []string{"Alice", "bob", "carol"}) {
		t.Fatalf("mentions merge: %v", got)
	}

	// Nothing on either side stays nil (the store coerces nil to '{}').
	if got := mergeTags(nil, nil, 10); got != nil {
		t.Fatalf("nil+nil must stay nil, got %v", got)
	}

	// The cap bounds the union.
	many := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		many = append(many, strings.Repeat("x", i+1))
	}
	if got := mergeTags(many[:20], many[20:], 25); len(got) != 25 {
		t.Fatalf("cap: got %d", len(got))
	}
}

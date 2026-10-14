package postgres

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNormaliseHistoryQuery(t *testing.T) {
	cases := []struct {
		name, in, display, key string
		ok                     bool
	}{
		{"trimmed and collapsed", "  Red   Saree\t", "Red Saree", "red saree", true},
		{"control characters become spaces", "cats\x00and\ndogs", "cats and dogs", "cats and dogs", true},
		{"empty", "", "", "", false},
		{"whitespace only", " \t\n ", "", "", false},
		{"one rune is too short", " a ", "", "", false},
		{"two runes is enough", "ab", "ab", "ab", true},
		{"one multibyte rune is too short", "猫", "", "", false},
		{"two multibyte runes", "猫猫", "猫猫", "猫猫", true},
		{"hashtag kept as typed", "#Diwali", "#Diwali", "#diwali", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, k, ok := NormaliseHistoryQuery(tc.in)
			if ok != tc.ok || d != tc.display || k != tc.key {
				t.Fatalf("NormaliseHistoryQuery(%q) = (%q, %q, %v), want (%q, %q, %v)", tc.in, d, k, ok, tc.display, tc.key, tc.ok)
			}
		})
	}
}

func TestNormaliseHistoryQuery_SameKeyForCaseAndSpacing(t *testing.T) {
	_, a, _ := NormaliseHistoryQuery("Cooking  Videos")
	_, b, _ := NormaliseHistoryQuery(" cooking videos ")
	if a != b {
		t.Fatalf("keys differ: %q vs %q — these must dedupe", a, b)
	}
}

func TestNormaliseHistoryQuery_CapsLength(t *testing.T) {
	d, k, ok := NormaliseHistoryQuery(strings.Repeat("é", MaxHistoryQueryRunes+50))
	if !ok || utf8.RuneCountInString(d) != MaxHistoryQueryRunes || utf8.RuneCountInString(k) != MaxHistoryQueryRunes {
		t.Fatalf("want %d runes, got display=%d key=%d ok=%v", MaxHistoryQueryRunes, utf8.RuneCountInString(d), utf8.RuneCountInString(k), ok)
	}
}

func TestIsSafePopularQuery(t *testing.T) {
	safe := []string{"cats", "diwali recipes", "iphone 15", "#travel", "@momentum", "top 10 songs 2026", "ipl 2026 final"}
	unsafe := []string{
		"x",
		"ravi.k@example.com",
		"contact ravi@mail.co.in",
		"9876543210",
		"+91 98765 43210",
		"040-1234567",
		"call (040) 123 4567",
		"abcde1234f",
		"ABCDE1234F pan",
	}
	for _, q := range safe {
		if !IsSafePopularQuery(q) {
			t.Errorf("IsSafePopularQuery(%q) = false, want true", q)
		}
	}
	for _, q := range unsafe {
		if IsSafePopularQuery(q) {
			t.Errorf("IsSafePopularQuery(%q) = true, want false (must never be shown to others)", q)
		}
	}
}

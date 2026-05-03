package service

import (
	"strings"
	"testing"
	"time"
)

// TestParseCohortMonth_Valid covers the happy path.
func TestParseCohortMonth_Valid(t *testing.T) {
	got, err := parseCohortMonth("2026-04")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestParseCohortMonth_RequiresValue verifies the empty-string guard.
func TestParseCohortMonth_RequiresValue(t *testing.T) {
	if _, err := parseCohortMonth(""); err == nil {
		t.Errorf("empty string should error")
	} else if !strings.HasPrefix(err.Error(), "invalid:") {
		t.Errorf("err = %q, want invalid: prefix", err)
	}
}

// TestParseCohortMonth_RejectsBadFormat verifies the format guard.
func TestParseCohortMonth_RejectsBadFormat(t *testing.T) {
	for _, in := range []string{"april 2026", "2026-13", "26-04", "2026/04"} {
		if _, err := parseCohortMonth(in); err == nil {
			t.Errorf("%q should have failed", in)
		}
	}
}

// TestParseCohortMonth_TrimsWhitespace handles leading/trailing space.
func TestParseCohortMonth_TrimsWhitespace(t *testing.T) {
	got, err := parseCohortMonth("  2026-04  ")
	if err != nil {
		t.Fatalf("trim: %v", err)
	}
	if got.Year() != 2026 || got.Month() != 4 {
		t.Errorf("got %v", got)
	}
}

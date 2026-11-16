package store

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The parts of the keyset id lists that need no database: the cursor's wire
// form and how limit+1 rows become a page. The SQL itself is exercised by
// id_pages_integration_test.go (CI).

func TestListCursor_RoundTripsToTheMicrosecond(t *testing.T) {
	at := time.Date(2026, 11, 16, 9, 30, 0, 123456000, time.UTC)
	id := uuid.MustParse("7d9f1c1e-55a0-4a53-9a55-0d5a3e1f0c11")
	wire := ListCursor{CreatedAt: at, ID: id}.String()
	if want := "1794821400123456:7d9f1c1e-55a0-4a53-9a55-0d5a3e1f0c11"; wire != want {
		t.Fatalf("wire form = %q, want %q", wire, want)
	}
	got, err := ParseListCursor(wire)
	if err != nil {
		t.Fatal(err)
	}
	if !got.CreatedAt.Equal(at) || got.ID != id {
		t.Fatalf("round trip = %v %v, want %v %v", got.CreatedAt, got.ID, at, id)
	}
}

func TestParseListCursor_RefusesEverythingItDidNotIssue(t *testing.T) {
	id := "7d9f1c1e-55a0-4a53-9a55-0d5a3e1f0c11"
	for name, raw := range map[string]string{
		"an offset (the old progress form)": "300",
		"no separator":                      "1794821400123456",
		"no id":                             "1794821400123456:",
		"no time":                           ":" + id,
		"time not a number":                 "yesterday:" + id,
		"negative time":                     "-5:" + id,
		"id not a uuid":                     "1794821400123456:not-a-uuid",
		"id without hyphens":                "1794821400123456:7d9f1c1e55a04a539a550d5a3e1f0c11",
		"id in braces":                      "1794821400123456:{" + id + "}",
		"trailing junk":                     "1794821400123456:" + id + ":x",
		"sql":                               "0:" + id + "' OR '1'='1",
	} {
		if _, err := ParseListCursor(raw); !errors.Is(err, ErrInvalidListCursor) {
			t.Errorf("%s (%q): err = %v, want ErrInvalidListCursor", name, raw, err)
		}
	}
}

func TestClampIDPageLimit(t *testing.T) {
	for in, want := range map[int]int{-1: 100, 0: 100, 1: 1, 100: 100, 500: 500, 501: 500, 100000: 500} {
		if got := ClampIDPageLimit(in); got != want {
			t.Errorf("ClampIDPageLimit(%d) = %d, want %d", in, got, want)
		}
	}
}

func edgesAt(n int) []FollowEdge {
	base := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	out := make([]FollowEdge, n)
	for i := range out {
		out[i] = FollowEdge{UserID: uuid.New(), CreatedAt: base.Add(-time.Duration(i) * time.Second)}
	}
	return out
}

func TestCutIDPage(t *testing.T) {
	t.Run("a further row was read: the cursor is the last row RETURNED", func(t *testing.T) {
		edges := edgesAt(4) // limit 3, plus the look-ahead row
		ids, next := cutIDPage(edges, 3)
		if len(ids) != 3 {
			t.Fatalf("page holds %d ids, want 3", len(ids))
		}
		want := ListCursor{CreatedAt: edges[2].CreatedAt, ID: edges[2].UserID}.String()
		if next != want {
			t.Fatalf("next = %q, want the third row %q (the look-ahead row would skip it)", next, want)
		}
	})
	t.Run("exactly a full page and nothing after it is the last page", func(t *testing.T) {
		ids, next := cutIDPage(edgesAt(3), 3)
		if len(ids) != 3 || next != "" {
			t.Fatalf("got %d ids and next %q, want 3 and no cursor", len(ids), next)
		}
	})
	t.Run("short and empty pages", func(t *testing.T) {
		if ids, next := cutIDPage(edgesAt(1), 3); len(ids) != 1 || next != "" {
			t.Fatalf("short page: %d ids, next %q", len(ids), next)
		}
		ids, next := cutIDPage(nil, 3)
		if ids == nil || len(ids) != 0 || next != "" {
			t.Fatalf("empty page: ids %v (nil %v), next %q", ids, ids == nil, next)
		}
	})
}

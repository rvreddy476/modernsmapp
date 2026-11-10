// Match list paging (10 Nov 2026): the cursor format and the page cut. No
// database; matches_page_it_test.go runs the keyset query itself.
package store

import (
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMatchCursor_RoundTripsExactly(t *testing.T) {
	at := time.Date(2026, 11, 10, 9, 30, 1, 123456000, time.FixedZone("IST", 19800))
	c := MatchCursor{SortAt: at, ID: uuid.New()}
	got, err := DecodeMatchCursor(c.Encode())
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.SortAt.Equal(at) || got.ID != c.ID {
		t.Fatalf("round trip = %+v, want %v / %v", got, at, c.ID)
	}
	if got.SortAt.Location() != time.UTC {
		t.Fatalf("decoded time is %v, want UTC", got.SortAt.Location())
	}
}

func TestDecodeMatchCursor_RefusesAnythingItDidNotIssue(t *testing.T) {
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	id := uuid.NewString()
	for name, raw := range map[string]string{
		"not base64":     "%%%",
		"padded base64":  base64.URLEncoding.EncodeToString([]byte("m1|2026-11-10T00:00:00Z|" + id + "x")),
		"wrong version":  enc("m0|2026-11-10T00:00:00Z|" + id),
		"two parts":      enc("m1|2026-11-10T00:00:00Z"),
		"four parts":     enc("m1|2026-11-10T00:00:00Z|" + id + "|x"),
		"bad time":       enc("m1|yesterday|" + id),
		"bad id":         enc("m1|2026-11-10T00:00:00Z|not-a-uuid"),
		"nil id":         enc("m1|2026-11-10T00:00:00Z|" + uuid.Nil.String()),
		"empty":          "",
		"plain uuid":     id,
		"offset no zone": enc("m1|2026-11-10T00:00:00|" + id),
	} {
		if c, err := DecodeMatchCursor(raw); !errors.Is(err, ErrInvalidMatchCursor) {
			t.Errorf("%s: decode = %+v, %v; want ErrInvalidMatchCursor", name, c, err)
		}
	}
}

func TestMatchSortAt_FollowsTheBucketOrder(t *testing.T) {
	matched := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	last := matched.Add(48 * time.Hour)
	withMsg := &Match{MatchedAt: matched, LastMessageAt: &last}
	noMsg := &Match{MatchedAt: matched}
	for _, status := range []string{"all", "active", "quiet", "anything"} {
		if got := MatchSortAt(withMsg, status); !got.Equal(last) {
			t.Errorf("%s with a message: sort at %v, want last_message_at %v", status, got, last)
		}
		if got := MatchSortAt(noMsg, status); !got.Equal(matched) {
			t.Errorf("%s without a message: sort at %v, want matched_at %v", status, got, matched)
		}
	}
	if got := MatchSortAt(withMsg, "sparks-waiting"); !got.Equal(matched) {
		t.Errorf("sparks-waiting: sort at %v, want matched_at %v", got, matched)
	}
	if matchListSort("sparks-waiting") != `m.matched_at` ||
		matchListSort("quiet") != `COALESCE(m.last_message_at, m.matched_at)` ||
		matchListSort("active") != `COALESCE(m.last_message_at, m.matched_at)` {
		t.Fatalf("SQL sort expressions drifted from MatchSortAt")
	}
}

func TestPageMatches_CutsAtLimitAndPointsAfterTheLastRow(t *testing.T) {
	base := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	rows := make([]*Match, 0, 4)
	for i := 0; i < 4; i++ {
		rows = append(rows, &Match{ID: uuid.New(), MatchedAt: base.Add(-time.Duration(i) * time.Hour)})
	}

	page, next := pageMatches(rows, 3, "active")
	if len(page) != 3 {
		t.Fatalf("page = %d rows, want 3", len(page))
	}
	if next == nil || next.ID != rows[2].ID || !next.SortAt.Equal(rows[2].MatchedAt) {
		t.Fatalf("next = %+v, want the third row %v", next, rows[2].ID)
	}

	// Exactly limit rows came back (limit+1 was asked for): the last page.
	page, next = pageMatches(rows[:3], 3, "active")
	if len(page) != 3 || next != nil {
		t.Fatalf("exact page: %d rows, next %+v; want 3 rows and no cursor", len(page), next)
	}
	page, next = pageMatches(nil, 3, "quiet")
	if len(page) != 0 || next != nil {
		t.Fatalf("empty: %d rows, next %+v", len(page), next)
	}
}

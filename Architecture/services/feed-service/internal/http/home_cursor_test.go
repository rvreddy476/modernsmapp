package http

import (
	"testing"
	"time"

	"github.com/atpost/feed-service/internal/store/scylla"
	"github.com/google/uuid"
)

func TestParseHomeCursor(t *testing.T) {
	if c, err := parseHomeCursor(""); err != nil || !c.FirstPage() {
		t.Fatalf("empty cursor = %+v, %v; want the first page", c, err)
	}

	// The token this handler issues round-trips to the exact timeline key.
	key := scylla.TimelineTS(uuid.New(), time.Now()).String()
	meta := rankedPageMeta(key)
	c, err := parseHomeCursor(meta.NextCursor)
	if err != nil || c.Token != key || c.Time != nil {
		t.Fatalf("issued cursor parsed to %+v, %v; want token %s", c, err, key)
	}

	// A client holding a page served before the keyset cursor shipped.
	legacy := "2026-10-13T09:30:15.123456789Z"
	c, err = parseHomeCursor(legacy)
	if err != nil || c.Time == nil || c.Token != "" {
		t.Fatalf("legacy cursor parsed to %+v, %v; want a time", c, err)
	}

	for _, bad := range []string{"not-a-cursor", "djI6eA", "djE6bm90LWEtdXVpZA"} {
		if _, err := parseHomeCursor(bad); err == nil {
			t.Errorf("cursor %q accepted", bad)
		}
	}
}

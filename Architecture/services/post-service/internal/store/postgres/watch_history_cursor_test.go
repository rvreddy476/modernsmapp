package postgres

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// The history cursor is opaque to the client: base64url of the keyset
// position (last_watched_at, post_id). It must survive a round trip at
// microsecond precision (what timestamptz keeps) and reject anything the
// service did not issue, so a tampered cursor is a 400 and never a query
// with a zero time that silently restarts the page from the top.
func TestWatchHistoryCursorRoundTrip(t *testing.T) {
	at := time.Date(2026, 9, 12, 10, 30, 0, 123456000, time.UTC)
	id := uuid.New()
	raw := encodeWatchHistoryCursor(at, id)

	gotAt, gotID, ok, err := decodeWatchHistoryCursor(raw)
	if err != nil || !ok {
		t.Fatalf("decode: ok=%v err=%v", ok, err)
	}
	if !gotAt.Equal(at) || gotID != id {
		t.Fatalf("round trip lost the position: got (%v, %s) want (%v, %s)", gotAt, gotID, at, id)
	}

	if _, _, ok, err := decodeWatchHistoryCursor(""); err != nil || ok {
		t.Fatalf("empty cursor must mean first page: ok=%v err=%v", ok, err)
	}
	for _, bad := range []string{"not-base64!", "bm90LWEtY3Vyc29y", raw + "x"} {
		if _, _, _, err := decodeWatchHistoryCursor(bad); err == nil {
			t.Errorf("cursor %q accepted; want ErrInvalidWatchHistoryCursor", bad)
		}
	}
}

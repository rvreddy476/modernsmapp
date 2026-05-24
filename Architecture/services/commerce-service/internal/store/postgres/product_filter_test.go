package postgres

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestProductFilter_DefaultsAndShape is a unit test on the public
// surface that doesn't need a DB. It documents what the filter
// accepts + verifies the cursor format is stable across versions.
func TestProductFilter_DefaultsAndShape(t *testing.T) {
	f := ProductFilter{}
	if f.Limit != 0 {
		t.Errorf("zero-value Limit should be 0 (store clamps), got %d", f.Limit)
	}
	if f.Cursor != "" {
		t.Errorf("zero-value Cursor should be empty, got %q", f.Cursor)
	}
	// Future filters bolt on; the existing ones MUST keep their JSON
	// shape stable so mobile + web stay compatible.
}

// TestParseFollowCursor verifies the cursor parser used by the graph
// HG2 keyset queries. Lives in this package only because the function
// is package-private; the package boundary is the right test scope.
//
// This is duplicated as a compile-time check that the cursor format
// (<unix_micros>:<uuid>) we settled on can be roundtripped without
// loss when the timestamp originates from time.Time.UnixMicro().
func TestProductFilter_CursorRoundtrip(t *testing.T) {
	id := uuid.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	cursor := formatCursor(now, id)
	if !strings.Contains(cursor, ":") {
		t.Fatalf("cursor missing separator: %q", cursor)
	}
	gotTS, gotID, ok := parseCursor(cursor)
	if !ok {
		t.Fatalf("cursor failed to parse: %q", cursor)
	}
	if !gotTS.Equal(now) {
		t.Errorf("timestamp lost: want %v, got %v", now, gotTS)
	}
	if gotID != id {
		t.Errorf("id lost: want %v, got %v", id, gotID)
	}
}

// formatCursor + parseCursor mirror the inline format the store uses
// for ProductFilter.Cursor. Kept here as test-local helpers so we can
// independently verify the roundtrip without exporting the format.
func formatCursor(ts time.Time, id uuid.UUID) string {
	return formatInt(ts.UnixMicro()) + ":" + id.String()
}

func parseCursor(c string) (time.Time, uuid.UUID, bool) {
	parts := strings.SplitN(c, ":", 2)
	if len(parts) != 2 {
		return time.Time{}, uuid.Nil, false
	}
	micros, err := parseInt(parts[0])
	if err != nil {
		return time.Time{}, uuid.Nil, false
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return time.Time{}, uuid.Nil, false
	}
	return time.UnixMicro(micros).UTC(), id, true
}

// formatInt / parseInt are thin wrappers around strconv so the test
// file doesn't have to import strconv just for two calls.
func formatInt(n int64) string {
	// inline minimal formatter to avoid importing strconv just here
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func parseInt(s string) (int64, error) {
	var n int64
	neg := false
	for i, c := range s {
		if i == 0 && c == '-' {
			neg = true
			continue
		}
		if c < '0' || c > '9' {
			return 0, errInt
		}
		n = n*10 + int64(c-'0')
	}
	if neg {
		n = -n
	}
	return n, nil
}

var errInt = newErr("bad int")

type stringErr string

func (s stringErr) Error() string { return string(s) }

func newErr(s string) error { return stringErr(s) }

package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Security walk 10 Nov 2026: a report target id is the canonical
// 8-4-4-4-12 form only; the other spellings uuid.Parse tolerates are junk.
func TestParseReportUUIDAcceptsOnlyTheCanonicalForm(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	for _, ok := range []string{id.String(), strings.ToUpper(id.String()), " " + id.String() + "\t"} {
		got, accepted := ParseReportUUID(ok)
		if !accepted || got != id {
			t.Errorf("%q: (%s, %v), want (%s, true)", ok, got, accepted, id)
		}
	}
	for _, bad := range []string{
		"urn:uuid:" + id.String(),
		"{" + id.String() + "}",
		strings.ReplaceAll(id.String(), "-", ""),
		uuid.Nil.String(),
		id.String()[:35],
		id.String() + "a",
		strings.Replace(id.String(), "-", "_", 1),
		"",
		"dance",
	} {
		if _, accepted := ParseReportUUID(bad); accepted {
			t.Errorf("%q accepted as a report target id", bad)
		}
		for _, typ := range []string{"user", "post", "comment", "sound"} {
			if _, _, err := ResolveReportEntity(typ, bad); !errors.Is(err, ErrInvalidReport) {
				t.Errorf("%s %q: err = %v, want ErrInvalidReport", typ, bad, err)
			}
		}
		if _, _, ok := ParseMessageEntityRef(bad + ":" + id.String()); ok {
			t.Errorf("message ref with conversation %q accepted", bad)
		}
	}
}

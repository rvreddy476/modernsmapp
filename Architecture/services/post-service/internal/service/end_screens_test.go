package service

import (
	"encoding/json"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// End screens (2026-09-29): the pure pieces the routes are built from. The
// routes themselves are driven in internal/http/end_screens_routes_test.go
// and against Postgres in end_screens_integration_test.go.

func TestEndScreenSlotPlaces(t *testing.T) {
	near := func(a, b float64) bool { return a-b < 1e-9 && b-a < 1e-9 }
	circleH := 0.20 * 16 / 9
	for _, tc := range []struct {
		kind string
		slot int
		want EndScreenPosition
	}{
		{"video", 0, EndScreenPosition{X: 0.05, Y: 0.10, W: 0.30}},
		{"playlist", 1, EndScreenPosition{X: 0.65, Y: 0.10, W: 0.30}},
		{"external_link", 2, EndScreenPosition{X: 0.05, Y: 0.60, W: 0.30}},
		{"video", 3, EndScreenPosition{X: 0.65, Y: 0.60, W: 0.30}},
		{"channel_subscribe", 0, EndScreenPosition{X: 0.05, Y: 0.10, W: 0.20}},
		{"channel", 1, EndScreenPosition{X: 0.75, Y: 0.10, W: 0.20}},
		{"channel_subscribe", 2, EndScreenPosition{X: 0.05, Y: 0.90 - circleH, W: 0.20}},
		{"channel", 3, EndScreenPosition{X: 0.75, Y: 0.90 - circleH, W: 0.20}},
		{"video", 5, EndScreenPosition{X: 0.65, Y: 0.10, W: 0.30}}, // wraps
	} {
		got := EndScreenSlotPlace(tc.kind, tc.slot)
		if !near(got.X, tc.want.X) || !near(got.Y, tc.want.Y) || !near(got.W, tc.want.W) {
			t.Fatalf("%s slot %d: %+v want %+v", tc.kind, tc.slot, got, tc.want)
		}
		if !positionInFrame(tc.kind, got) {
			t.Fatalf("%s slot %d is not inside the frame: %+v", tc.kind, tc.slot, got)
		}
	}
	// Every pair of slots, of any two kinds, passes the overlap rule.
	kinds := []string{"video", "channel_subscribe"}
	for a := 0; a < 4; a++ {
		for b := a + 1; b < 4; b++ {
			for _, ka := range kinds {
				for _, kb := range kinds {
					if boxesOverlapTooMuch(ka, EndScreenSlotPlace(ka, a), kb, EndScreenSlotPlace(kb, b)) {
						t.Fatalf("slots %d (%s) and %d (%s) overlap", a, ka, b, kb)
					}
				}
			}
		}
	}
}

func TestParseEndScreenPosition(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		index int
		want  EndScreenPosition
	}{
		{`{"x":0.2,"y":0.3,"w":0.25}`, 0, EndScreenPosition{X: 0.2, Y: 0.3, W: 0.25}},
		{`{"slot":1}`, 3, EndScreenSlotPlace("video", 1)},
		{`{"slot":9}`, 2, EndScreenSlotPlace("video", 2)},
		{`{"x":0.2}`, 3, EndScreenSlotPlace("video", 3)},
		{`"nonsense"`, 1, EndScreenSlotPlace("video", 1)},
		{``, 0, EndScreenSlotPlace("video", 0)},
	} {
		if got := ParseEndScreenPosition(json.RawMessage(tc.raw), "video", tc.index); got != tc.want {
			t.Fatalf("%s at %d: %+v want %+v", tc.raw, tc.index, got, tc.want)
		}
	}
}

func TestPopularOrder(t *testing.T) {
	a, b, c, d := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	got := popularOrder([]uuid.UUID{a, b, c, d}, map[uuid.UUID]int64{b: 5, c: 9, d: 5})
	want := []uuid.UUID{c, b, d, a} // most viewed first; the tie keeps newest-first order
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order %v want %v", got, want)
		}
	}
}

func TestCheckTargetURL(t *testing.T) {
	long := "https://a.b/"
	for len(long) <= MaxTargetURL {
		long += "x"
	}
	for raw, ok := range map[string]bool{
		"https://example.com":      true,
		" https://example.com/a ":  true,
		"HTTPS://Example.com":      true,
		"http://example.com":       false,
		"ftp://example.com":        false,
		"https://":                 false,
		"https:///path":            false,
		"example.com":              false,
		"javascript:alert(1)":      false,
		long:                       false,
		long[:MaxTargetURL]:        true,
		"https://user@example.com": true,
	} {
		raw := raw
		if _, got := checkTargetURL(&raw); got != ok {
			t.Fatalf("%.40q: %v want %v", raw, got, ok)
		}
	}
	if _, ok := checkTargetURL(nil); ok {
		t.Fatal("nil url accepted")
	}
	if d := linkDomain("https://www.Example.COM/x"); d != "example.com" {
		t.Fatalf("domain %q", d)
	}
}

func TestStatsViewClickRate(t *testing.T) {
	if v := statsView(postgres.ElementStats{}); v.ClickRate != 0 {
		t.Fatalf("no impressions: %+v", v)
	}
	if v := statsView(postgres.ElementStats{Impressions: 3, Clicks: 1}); v.ClickRate != 0.3333 {
		t.Fatalf("1/3: %+v", v)
	}
}

func TestStatViewerKey(t *testing.T) {
	id := uuid.New()
	if k := (StatViewer{UserID: &id, AnonID: "ip"}).key(); k != "u:"+id.String() {
		t.Fatalf("signed in: %q", k)
	}
	if k := (StatViewer{AnonID: "ip"}).key(); k != "a:ip" {
		t.Fatalf("anonymous: %q", k)
	}
	if k := (StatViewer{}).key(); k != "" {
		t.Fatalf("nobody: %q", k)
	}
}

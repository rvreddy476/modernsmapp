package search

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// MTube video filters (tube.go): the parser's allowlist and the query each
// option produces. Pure — the bodies are inspected, not sent.

func TestParseVideoSearchOptions(t *testing.T) {
	good := []struct {
		sort, duration, date, features string
		want                           VideoSearchOptions
	}{
		{"", "", "", "", VideoSearchOptions{Sort: "relevance"}},
		{"relevance", "", "", "", VideoSearchOptions{Sort: "relevance"}},
		{"VIEWS", "Short", " today ", "CC,hd", VideoSearchOptions{Sort: "views", Duration: "short", Date: "today", Features: []string{"cc", "hd"}}},
		{"date", "long", "year", "4k,4k,,cc", VideoSearchOptions{Sort: "date", Duration: "long", Date: "year", Features: []string{"4k", "cc"}}},
	}
	for _, tc := range good {
		got, err := ParseVideoSearchOptions(tc.sort, tc.duration, tc.date, tc.features)
		if err != nil {
			t.Fatalf("(%q,%q,%q,%q): %v", tc.sort, tc.duration, tc.date, tc.features, err)
		}
		if got.Sort != tc.want.Sort || got.Duration != tc.want.Duration || got.Date != tc.want.Date || strings.Join(got.Features, ",") != strings.Join(tc.want.Features, ",") {
			t.Fatalf("(%q,%q,%q,%q) = %+v, want %+v", tc.sort, tc.duration, tc.date, tc.features, got, tc.want)
		}
	}
	bad := []struct{ sort, duration, date, features, param string }{
		{"trending", "", "", "", "sort"},
		{"", "tiny", "", "", "duration"},
		{"", "", "yesterday", "", "date"},
		{"", "", "", "8k", "features"},
		{"", "", "", "cc;hd", "features"},
	}
	for _, tc := range bad {
		_, err := ParseVideoSearchOptions(tc.sort, tc.duration, tc.date, tc.features)
		if err == nil || !strings.Contains(err.Error(), "'"+tc.param+"'") {
			t.Fatalf("(%q,%q,%q,%q): err=%v, want one naming %s", tc.sort, tc.duration, tc.date, tc.features, err, tc.param)
		}
	}
	if !(VideoSearchOptions{Sort: "relevance"}).IsZero() || (VideoSearchOptions{Sort: "views"}).IsZero() || (VideoSearchOptions{Features: []string{"cc"}}).IsZero() {
		t.Fatal("IsZero: relevance with nothing else is the plain search; anything else is not")
	}
}

// filtersOf digs the bool.filter list out of a query body.
func filtersOf(t *testing.T, q map[string]interface{}) []map[string]interface{} {
	t.Helper()
	raw, _ := json.Marshal(q)
	var body struct {
		Query struct {
			Bool struct {
				Filter []map[string]interface{} `json:"filter"`
			} `json:"bool"`
		} `json:"query"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	return body.Query.Bool.Filter
}

func hasFilter(filters []map[string]interface{}, kind, field string) map[string]interface{} {
	for _, f := range filters {
		if clause, ok := f[kind].(map[string]interface{}); ok {
			if v, ok := clause[field]; ok {
				if m, ok := v.(map[string]interface{}); ok {
					return m
				}
				return map[string]interface{}{"value": v}
			}
		}
	}
	return nil
}

func TestBuildVideoSearchQuery_FiltersAndSort(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	types := []string{"long_video", "video"}

	t.Run("no options sends the plain query", func(t *testing.T) {
		q := BuildVideoSearchQuery("cats", types, VideoSearchOptions{Sort: VideoSortRelevance}, 20)
		filters := filtersOf(t, q)
		if hasFilter(filters, "terms", "content_type") == nil {
			t.Fatalf("content-type terms missing: %v", filters)
		}
		if len(filters) != len(publicApprovedFilter())+1 {
			t.Fatalf("plain video query carries %d filters, want public-approved + content types only: %v", len(filters), filters)
		}
		if _, ok := q["sort"]; ok {
			t.Fatal("relevance must not add a sort block")
		}
	})

	t.Run("duration buckets", func(t *testing.T) {
		short := hasFilter(filtersOf(t, BuildVideoSearchQuery("q", types, VideoSearchOptions{Duration: VideoDurationShort}, 20)), "range", "duration_ms")
		if short == nil || short["lt"].(float64) != 240000 || short["gte"].(float64) != 1 {
			t.Fatalf("short = %v, want 1 <= duration_ms < 240000 (unknown 0 excluded)", short)
		}
		medium := hasFilter(filtersOf(t, BuildVideoSearchQuery("q", types, VideoSearchOptions{Duration: VideoDurationMedium}, 20)), "range", "duration_ms")
		if medium == nil || medium["gte"].(float64) != 240000 || medium["lte"].(float64) != 1200000 {
			t.Fatalf("medium = %v, want 240000..1200000", medium)
		}
		long := hasFilter(filtersOf(t, BuildVideoSearchQuery("q", types, VideoSearchOptions{Duration: VideoDurationLong}, 20)), "range", "duration_ms")
		if long == nil || long["gt"].(float64) != 1200000 {
			t.Fatalf("long = %v, want > 1200000", long)
		}
	})

	t.Run("date windows are anchored on now", func(t *testing.T) {
		cases := map[string]string{
			VideoDateHour:  "2026-09-27T11:00:00Z",
			VideoDateToday: "2026-09-26T12:00:00Z",
			VideoDateWeek:  "2026-09-20T12:00:00Z",
			VideoDateMonth: "2026-08-27T12:00:00Z",
			VideoDateYear:  "2025-09-27T12:00:00Z",
		}
		for date, want := range cases {
			r := hasFilter(filtersOf(t, BuildVideoSearchQuery("q", types, VideoSearchOptions{Date: date, Now: now}, 20)), "range", "created_at")
			if r == nil || r["gte"] != want {
				t.Fatalf("date=%s: range = %v, want gte %s", date, r, want)
			}
		}
	})

	t.Run("features", func(t *testing.T) {
		filters := filtersOf(t, BuildVideoSearchQuery("q", types, VideoSearchOptions{Features: []string{"cc", "hd", "4k"}}, 20))
		if cc := hasFilter(filters, "term", "has_subtitles"); cc == nil || cc["value"] != true {
			t.Fatalf("cc = %v, want term has_subtitles:true", cc)
		}
		var heights []float64
		for _, f := range filters {
			if r, ok := f["range"].(map[string]interface{}); ok {
				if h, ok := r["height"].(map[string]interface{}); ok {
					heights = append(heights, h["gte"].(float64))
				}
			}
		}
		if len(heights) != 2 || heights[0] != 720 || heights[1] != 2160 {
			t.Fatalf("height ranges = %v, want [720 2160]", heights)
		}
	})

	t.Run("sorts", func(t *testing.T) {
		views := BuildVideoSearchQuery("q", types, VideoSearchOptions{Sort: VideoSortViews}, 20)["sort"].([]interface{})
		if first := views[0].(map[string]interface{}); first["view_count"] == nil {
			t.Fatalf("sort=views: first key = %v, want view_count", first)
		}
		date := BuildVideoSearchQuery("q", types, VideoSearchOptions{Sort: VideoSortDate}, 20)["sort"].([]interface{})
		if first := date[0].(map[string]interface{}); first["created_at"] == nil {
			t.Fatalf("sort=date: first key = %v, want created_at", first)
		}
	})
}

// The collections query only ever searches public playlists. This is the
// query-time half of the visibility rule; the index-time half is tested in
// reindex/tube_test.go.
func TestBuildTubeCollectionQuery_OnlyPublic(t *testing.T) {
	filters := filtersOf(t, BuildTubeCollectionQuery("mixes", 20, 0))
	vis := hasFilter(filters, "term", "visibility")
	if vis == nil || vis["value"] != "public" {
		t.Fatalf("collections query filters = %v, want term visibility:public.\n"+
			"Without it a playlist that went private after indexing stays discoverable.", filters)
	}
}

func TestBuildTubeChannelQuery_HandleAndName(t *testing.T) {
	raw, _ := json.Marshal(BuildTubeChannelQuery("@Cooking", 10, 20))
	body := string(raw)
	for _, want := range []string{`"from":20`, `"size":10`, `"handle":{"boost":6,"value":"cooking"}`, `"name^3"`, `"follower_count"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("channel query lacks %s: %s", want, body)
		}
	}
}

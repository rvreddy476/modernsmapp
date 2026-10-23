package search

import (
	"encoding/json"
	"strings"
	"testing"
)

// Video credits (2026-10-23, credits.go): the text match finds a credited
// name at twice the base weight and never a role; ?genre= / ?year= become
// term filters only when set; malformed values are refused by the parser
// the handler calls before the store.

func multiMatchFields(t *testing.T, q map[string]interface{}) []string {
	t.Helper()
	raw, _ := json.Marshal(q)
	var body struct {
		Query struct {
			Bool struct {
				Must []struct {
					MultiMatch struct {
						Fields []string `json:"fields"`
					} `json:"multi_match"`
				} `json:"must"`
			} `json:"bool"`
		} `json:"query"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Query.Bool.Must) == 0 {
		t.Fatalf("no must clause: %s", raw)
	}
	return body.Query.Bool.Must[0].MultiMatch.Fields
}

func TestPostSearchFields_MatchCreditsBoostedButNotRoles(t *testing.T) {
	types := []string{"long_video", "video"}
	for name, q := range map[string]map[string]interface{}{
		"posts":  BuildPostSearchQuery("Asha Rao", nil, CreditFilters{}, 20),
		"videos": BuildVideoSearchQuery("Asha Rao", types, VideoSearchOptions{}, 20),
	} {
		t.Run(name, func(t *testing.T) {
			fields := multiMatchFields(t, q)
			want := map[string]bool{"title^3": true, "text^2": true, "hashtags": true,
				"credit_names^2": true, "genres": true, "production_house": true}
			for _, f := range fields {
				if strings.HasPrefix(f, "credit_roles") {
					t.Fatalf("roles are matched (%v): a query for \"Director\" would return every directed video", fields)
				}
				delete(want, f)
			}
			if len(want) != 0 {
				t.Fatalf("fields %v lack %v", fields, want)
			}
		})
	}
}

func TestRankedPostsQuery_MatchesCreditedNames(t *testing.T) {
	q := buildFunctionScoreQuery(EntityPosts, "Asha Rao", RankedSearchOptions{Limit: 10})
	raw, _ := json.Marshal(q)
	if !strings.Contains(string(raw), `"credit_names^2"`) {
		t.Fatalf("ranked posts query does not match credited names: %s", raw)
	}
	if strings.Contains(string(raw), "credit_roles") {
		t.Fatalf("ranked posts query matches roles: %s", raw)
	}
}

func TestBuildPostSearchQuery_CreditFilters(t *testing.T) {
	t.Run("unset filters send the plain body", func(t *testing.T) {
		filters := filtersOf(t, BuildPostSearchQuery("q", nil, CreditFilters{}, 20))
		if len(filters) != len(publicApprovedFilter()) {
			t.Fatalf("plain posts query carries %d filters, want the public-approved pair only: %v", len(filters), filters)
		}
	})
	t.Run("genre", func(t *testing.T) {
		filters := filtersOf(t, BuildPostSearchQuery("q", nil, CreditFilters{Genre: "sci-fi"}, 20))
		if g := hasFilter(filters, "term", "genres"); g == nil || g["value"] != "sci-fi" {
			t.Fatalf("genre filter = %v in %v", g, filters)
		}
		if hasFilter(filters, "term", "release_year") != nil {
			t.Fatalf("year filter present without ?year=: %v", filters)
		}
	})
	t.Run("year", func(t *testing.T) {
		filters := filtersOf(t, BuildPostSearchQuery("q", nil, CreditFilters{Year: 2024}, 20))
		if y := hasFilter(filters, "term", "release_year"); y == nil || y["value"] != float64(2024) {
			t.Fatalf("year filter = %v in %v", y, filters)
		}
		if hasFilter(filters, "term", "genres") != nil {
			t.Fatalf("genre filter present without ?genre=: %v", filters)
		}
	})
	t.Run("combined with the video options and kept public-approved", func(t *testing.T) {
		q := BuildVideoSearchQueryWithCredits("q", []string{"long_video", "video"},
			VideoSearchOptions{Duration: VideoDurationLong, Sort: VideoSortViews}, CreditFilters{Genre: "drama", Year: 2023}, 20)
		filters := filtersOf(t, q)
		for _, c := range []struct{ kind, field string }{
			{"term", "visibility"}, {"term", "review_status"}, {"terms", "content_type"},
			{"term", "genres"}, {"term", "release_year"}, {"range", "duration_ms"},
		} {
			if hasFilter(filters, c.kind, c.field) == nil {
				t.Fatalf("filter %s %s missing: %v", c.kind, c.field, filters)
			}
		}
		if _, ok := q["sort"]; !ok {
			t.Fatal("sort=views lost when combined with credit filters")
		}
	})
}

func TestParseCreditFilters(t *testing.T) {
	ok := []struct {
		genre, year string
		want        CreditFilters
	}{
		{"", "", CreditFilters{}},
		{" Drama ", "", CreditFilters{Genre: "drama"}},
		{"sci-fi", "2024", CreditFilters{Genre: "sci-fi", Year: 2024}},
		{"", "1900", CreditFilters{Year: 1900}},
		{"", "2100", CreditFilters{Year: 2100}},
	}
	for _, c := range ok {
		got, err := ParseCreditFilters(c.genre, c.year)
		if err != nil || got != c.want {
			t.Fatalf("ParseCreditFilters(%q, %q) = %+v, %v; want %+v", c.genre, c.year, got, err, c.want)
		}
	}
	bad := []struct{ genre, year, param string }{
		{"drama thriller", "", "'genre'"},
		{"-drama", "", "'genre'"},
		{"drama*", "", "'genre'"},
		{strings.Repeat("a", 33), "", "'genre'"},
		{"", "1899", "'year'"},
		{"", "2101", "'year'"},
		{"", "24", "'year'"},
		{"", "twenty", "'year'"},
	}
	for _, c := range bad {
		if _, err := ParseCreditFilters(c.genre, c.year); err == nil || !strings.Contains(err.Error(), c.param) {
			t.Fatalf("ParseCreditFilters(%q, %q) err = %v; want a %s error", c.genre, c.year, err, c.param)
		}
	}
}

func TestCreditsMapping_Types(t *testing.T) {
	var body struct {
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(creditsMappingJSON), &body); err != nil {
		t.Fatalf("credits mapping is not JSON: %v", err)
	}
	want := map[string]string{"credit_names": "text", "credit_roles": "text", "genres": "keyword",
		"release_year": "integer", "production_house": "text"}
	for field, typ := range want {
		if got := body.Properties[field].Type; got != typ {
			t.Fatalf("%s mapped as %q, want %q", field, got, typ)
		}
	}
}

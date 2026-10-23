package search

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"

	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
)

// Video credits (cast & crew on a long video, 2026-10-23).
//
// post-service puts five fields on the post events of a long video —
// credit_names, credit_roles, genres, release_year, production_house — and
// nothing for any other kind (events.PostCreditSearchFields). They land on
// the posts_v1 document here, so:
//
//   - GET /v1/search/posts?q= matches a credited name (boosted 2x, the same
//     weight as the caption), a genre slug and the production house. A
//     query that equals a credited name returns those videos. Roles are
//     stored for display and future facets but are NOT matched: "Director"
//     as a query must not return every directed video.
//   - ?genre=<slug> and ?year=<YYYY> narrow the result. Only long videos
//     carry the fields, so either filter narrows to long videos whatever
//     ?type= says; both combine with the MTube video options.
//
// Every credit name is plain text in the index, whatever the tag's consent
// status: the uploader typed it, and the index never learns who was tagged
// (no user id is sent). The mapping is layered onto an existing posts_v1
// on boot (putCreditsMapping, additive and idempotent like every field
// before it); existing long videos get their values from the next posts
// reindex (cmd/backfill -entity posts) or their next eligibility event.

// postTextFields is the one multi_match field list for the post searches
// (SearchPostsFiltered and BuildVideoSearchQuery), so the two can never
// weigh a credited name differently. Title and caption keep their weights.
var postTextFields = []string{"title^3", "text^2", "hashtags", "credit_names^2", "genres", "production_house"}

// PostTextFields returns a copy of the post multi_match fields, for tests
// and callers that build their own body.
func PostTextFields() []string {
	return append([]string(nil), postTextFields...)
}

// CreditFilters is the parsed ?genre= / ?year= pair. The zero value
// filters nothing.
type CreditFilters struct {
	Genre string
	Year  int
}

// IsZero reports whether the filters change anything.
func (f CreditFilters) IsZero() bool { return f.Genre == "" && f.Year == 0 }

// Release-year bounds the filter accepts. post-service bounds the stored
// value to 1900..current year + 1; the filter is a little wider so a client
// never gets a 400 for a year the server could store next January.
const (
	creditYearMin = 1900
	creditYearMax = 2100
)

// genreSlugPattern is the shape of a post-service genre slug (action,
// sci-fi, ...). The filter does not know the list itself — post-service
// owns it — so an unknown slug that is well-formed is an empty page, not
// a 400.
var genreSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// ParseCreditFilters validates the raw query values. Empty is fine; a
// malformed value is an error whose message names the parameter, suitable
// for a 400 body.
func ParseCreditFilters(genre, year string) (CreditFilters, error) {
	var f CreditFilters
	if g := strings.ToLower(strings.TrimSpace(genre)); g != "" {
		if !genreSlugPattern.MatchString(g) {
			return f, fmt.Errorf("Parameter 'genre' must be a genre slug such as drama or sci-fi")
		}
		f.Genre = g
	}
	if y := strings.TrimSpace(year); y != "" {
		n, err := strconv.Atoi(y)
		if err != nil || n < creditYearMin || n > creditYearMax {
			return CreditFilters{}, fmt.Errorf("Parameter 'year' must be a year between %d and %d", creditYearMin, creditYearMax)
		}
		f.Year = n
	}
	return f, nil
}

// creditFilterClauses turns the filters into bool.filter clauses; nil for
// the zero value, so an unfiltered query body is unchanged.
func creditFilterClauses(f CreditFilters) []map[string]interface{} {
	var out []map[string]interface{}
	if f.Genre != "" {
		out = append(out, map[string]interface{}{"term": map[string]interface{}{"genres": f.Genre}})
	}
	if f.Year != 0 {
		out = append(out, map[string]interface{}{"term": map[string]interface{}{"release_year": f.Year}})
	}
	return out
}

// BuildPostSearchQuery is the body SearchPostsWithFilters sends: the text
// match over postTextFields, the mandatory public-approved filter, the
// optional content-type terms and the credit filters. Pure, for the tests.
func BuildPostSearchQuery(query string, contentTypes []string, f CreditFilters, limit int) map[string]interface{} {
	filter := publicApprovedFilter()
	if len(contentTypes) > 0 {
		filter = append(filter, map[string]interface{}{
			"terms": map[string]interface{}{"content_type": contentTypes},
		})
	}
	filter = append(filter, creditFilterClauses(f)...)
	return map[string]interface{}{
		"size": limit,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{"multi_match": map[string]interface{}{
						"query":  query,
						"fields": PostTextFields(),
					}},
				},
				"filter": filter,
			},
		},
	}
}

// SearchPostsWithFilters is SearchPostsFiltered with the credit filters.
// With zero filters it sends exactly what SearchPostsFiltered sends.
func (s *Store) SearchPostsWithFilters(ctx context.Context, query string, contentTypes []string, f CreditFilters, limit int) ([]PostDoc, error) {
	return s.execPostSearch(ctx, BuildPostSearchQuery(query, contentTypes, f, limit))
}

// putCreditsMapping adds the five credit fields to an existing posts
// index. Additive and idempotent, like putVideoFilterMapping. credit_names
// carries a keyword subfield (the house pattern for display text, as on
// title) so an exact-name facet can be added without a reindex.
func (s *Store) putCreditsMapping(ctx context.Context, index string) {
	req := opensearchapi.IndicesPutMappingRequest{
		Index: []string{index},
		Body:  strings.NewReader(creditsMappingJSON),
	}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		slog.Warn("opensearch: put credits mapping failed", "index", index, "err", err)
		return
	}
	defer res.Body.Close()
	if res.IsError() {
		slog.Warn("opensearch: put credits mapping rejected", "index", index, "status", res.StatusCode)
	}
}

// creditsMappingJSON is the put-mapping body (also asserted by the tests).
const creditsMappingJSON = `{"properties":{
	"credit_names":     {"type":"text","fields":{"keyword":{"type":"keyword","ignore_above":256}}},
	"credit_roles":     {"type":"text"},
	"genres":           {"type":"keyword"},
	"release_year":     {"type":"integer"},
	"production_house": {"type":"text"}
}}`

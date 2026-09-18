package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/atpost/post-service/internal/service"
	"github.com/gin-gonic/gin"
)

// /v1/posts/trending was its neighbour's odd twin (2026-09-18).
//
// Three differences from /v1/posts/recent, each costing a client a special
// case: the cursor sat inside data instead of meta.next_cursor; content_type
// took repeated query params where /recent takes a comma-separated list; and
// the legacy spelling "video" was a 400 here and a long_video filter there.
//
// All three now match /recent, and the OLD forms still work — these tests
// pin both, because "keeps the cursor in data" is now a compatibility
// promise rather than an accident.

func TestTrendingContentTypeAcceptsBothCommaListAndRepeatedParams(t *testing.T) {
	cases := []struct {
		name string
		raws []string
		want []string
	}{
		{"the old form: repeated params", []string{"long_video", "flick"}, []string{"long_video", "flick"}},
		{"the /recent form: one comma-separated list", []string{"long_video,flick"}, []string{"long_video", "flick"}},
		{"both at once", []string{"long_video,flick", "poll"}, []string{"long_video", "flick", "poll"}},
		{"duplicates collapse", []string{"flick,flick", "flick"}, []string{"flick"}},
		{"no filter", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mergeContentTypeFilters(tc.raws)
			if err != nil {
				t.Fatalf("mergeContentTypeFilters(%v): %v", tc.raws, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("mergeContentTypeFilters(%v) = %v, want %v", tc.raws, got, tc.want)
			}
		})
	}
}

// "video" and "reel" are what /v1/posts/recent accepts and maps; trending
// answered 400 to both, so a client that filtered one list could not filter
// the other with the same string.
func TestTrendingContentTypeAcceptsLegacySpellings(t *testing.T) {
	for raw, want := range map[string]string{"video": "long_video", "reel": "flick"} {
		got, err := mergeContentTypeFilters([]string{raw})
		if err != nil {
			t.Fatalf("content_type=%q: %v — /v1/posts/recent accepts this spelling", raw, err)
		}
		if len(got) != 1 || got[0] != want {
			t.Fatalf("content_type=%q normalised to %v, want [%s]", raw, got, want)
		}
	}
	if _, err := mergeContentTypeFilters([]string{"nonsense"}); err == nil {
		t.Fatal("an unknown content_type must still be refused")
	}
}

// An unknown type reaches the wire as the 400 it always was — same status,
// same INVALID_CONTENT_TYPE code — so the relaxation did not turn a refusal
// into a silently unfiltered page.
func TestTrendingRejectsUnknownContentTypeOnTheWire(t *testing.T) {
	w := performVideoSeriesRequest(t, http.MethodGet, "/v1/posts/trending?content_type=nonsense", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("content_type=nonsense: status=%d want 400 (body %s)", w.Code, w.Body.String())
	}
	var body struct {
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	if body.Error == nil || body.Error.Code != "INVALID_CONTENT_TYPE" {
		t.Fatalf("error code = %+v, want INVALID_CONTENT_TYPE", body.Error)
	}
}

// The cursor is in meta.next_cursor, where every other list in this service
// puts it, AND still inside data, where this endpoint shipped it.
func TestTrendingPageCarriesCursorInMetaAndInData(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/posts/trending", nil)

	writeTrendingPage(c, []service.PostDetail{}, "cursor-abc")

	var body struct {
		Data struct {
			Items      []json.RawMessage `json:"items"`
			NextCursor string            `json:"next_cursor"`
		} `json:"data"`
		Meta *struct {
			NextCursor string `json:"next_cursor"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if body.Meta == nil || body.Meta.NextCursor != "cursor-abc" {
		t.Errorf("meta.next_cursor = %+v, want cursor-abc — this is where /v1/posts/recent puts it", body.Meta)
	}
	if body.Data.NextCursor != "cursor-abc" {
		t.Errorf("data.next_cursor = %q, want cursor-abc — the old placement must keep working", body.Data.NextCursor)
	}
	if body.Data.Items == nil {
		t.Error("data.items must stay an array, never null")
	}
}

// The last page carries no cursor in either place, rather than an empty
// meta a client would page into.
func TestTrendingLastPageHasNoCursorAnywhere(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/posts/trending", nil)

	writeTrendingPage(c, nil, "")

	var body struct {
		Data struct {
			NextCursor string `json:"next_cursor"`
		} `json:"data"`
		Meta *struct {
			NextCursor string `json:"next_cursor"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if body.Data.NextCursor != "" {
		t.Errorf("data.next_cursor = %q on the last page, want empty", body.Data.NextCursor)
	}
	if body.Meta != nil && body.Meta.NextCursor != "" {
		t.Errorf("meta.next_cursor = %q on the last page, want empty", body.Meta.NextCursor)
	}
}

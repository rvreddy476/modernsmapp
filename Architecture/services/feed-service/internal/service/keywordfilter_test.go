package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Filter keywords ("Content preferences → Filter keywords") — matcher
// semantics and the fail-closed lookup, mirroring blocksafety_test.go:
// a failed filter lookup must be an ERROR, never "nobody filtered".

// ─── Whole-word matcher ──────────────────────────────────────────────────────

func TestContainsWholeWord(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		keyword string
		want    bool
	}{
		{"simple word", "i hate spoilers so much", "spoilers", true},
		{"word at start", "spoilers ahead", "spoilers", true},
		{"word at end", "beware of spoilers", "spoilers", true},
		{"whole text is the word", "spoilers", "spoilers", true},
		{"substring of a longer word does NOT match", "the concatenation works", "cat", false},
		{"prefix of a longer word does NOT match", "this category rocks", "cat", false},
		{"punctuation is a boundary", "no spoilers!", "spoilers", true},
		{"comma boundary", "spoilers, everywhere", "spoilers", true},
		{"hashtag form matches", "watch out #spoilers ahead", "spoilers", true},
		{"hashtag only", "#spoilers", "spoilers", true},
		{"digits are word runes", "abc123spoilers", "spoilers", false},
		{"multi-word phrase", "the game of thrones finale", "game of thrones", true},
		{"unicode keyword", "j'adore la crème brûlée", "crème", true},
		{"unicode boundary respected", "supercrèmeword", "crème", false},
		{"cyrillic word", "это спойлер конечно", "спойлер", true},
		{"cyrillic inside a word does not match", "этоспойлерслово", "спойлер", false},
		{"empty keyword never matches", "anything", "", false},
		{"second occurrence matches after bad first", "concat cat", "cat", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := containsWholeWord(tc.text, tc.keyword); got != tc.want {
				t.Fatalf("containsWholeWord(%q, %q) = %v, want %v", tc.text, tc.keyword, got, tc.want)
			}
		})
	}
}

func TestTextContainsAnyKeyword_CaseInsensitive(t *testing.T) {
	if !textContainsAnyKeyword("SPOILERS Ahead", []string{"spoilers"}) {
		t.Fatal("uppercase text must match a lowercase keyword")
	}
	if !textContainsAnyKeyword("Crème fraîche", []string{"crème"}) {
		t.Fatal("unicode case folding must match")
	}
}

// ─── Post-level filtering across text surfaces ───────────────────────────────

func hydratedWithText(text string) HydratedPost {
	return HydratedPost{ID: uuid.New(), AuthorID: uuid.New(), Text: text}
}

func TestFilterPostsByKeywords_DropsMatchingText(t *testing.T) {
	posts := []HydratedPost{
		hydratedWithText("a lovely day"),
		hydratedWithText("massive spoilers inside"),
		hydratedWithText("check out #spoilers now"),
		hydratedWithText("no relevant words"),
	}
	out := filterPostsByKeywords(posts, []string{"spoilers"})
	if len(out) != 2 {
		t.Fatalf("expected 2 surviving posts, got %d", len(out))
	}
	for _, p := range out {
		if textContainsAnyKeyword(p.Text, []string{"spoilers"}) {
			t.Fatal("a matching post survived the filter")
		}
	}
}

func TestFilterPostsByKeywords_MatchesRichText(t *testing.T) {
	p := hydratedWithText("caption is clean")
	p.RichText = json.RawMessage(`{"blocks":[{"type":"p","children":[{"text":"hidden spoilers here"}]}]}`)
	out := filterPostsByKeywords([]HydratedPost{p}, []string{"spoilers"})
	if len(out) != 0 {
		t.Fatal("a keyword inside rich_text must hide the post")
	}
}

func TestFilterPostsByKeywords_MatchesAltText(t *testing.T) {
	p := hydratedWithText("caption is clean")
	p.Media = []HydratedMedia{{MediaID: uuid.New(), Kind: "image", AltText: "a photo full of spoilers"}}
	out := filterPostsByKeywords([]HydratedPost{p}, []string{"spoilers"})
	if len(out) != 0 {
		t.Fatal("a keyword inside media alt_text must hide the post")
	}
}

func TestFilterPostsByKeywords_EmptySetIsPassThrough(t *testing.T) {
	posts := []HydratedPost{hydratedWithText("spoilers everywhere")}
	if out := filterPostsByKeywords(posts, nil); len(out) != 1 {
		t.Fatal("no keywords means no filtering")
	}
}

func TestFilterPostsByKeywords_RichTextSubstringDoesNotFalsePositive(t *testing.T) {
	// "cat" appears only inside longer words across every surface; the JSON
	// structure around rich text must not create phantom boundaries that
	// hide an innocent post.
	p := hydratedWithText("the concatenation category")
	p.RichText = json.RawMessage(`{"blocks":[{"text":"more concatenation"}]}`)
	out := filterPostsByKeywords([]HydratedPost{p}, []string{"cat"})
	if len(out) != 1 {
		t.Fatal("substrings of longer words must not hide a post")
	}
}

// ─── Fail-closed lookup (style of blocksafety_test.go) ───────────────────────

func newFeedServiceWithTrust(url string) *Service {
	return &Service{
		trustSafetyURL: url,
		trustClient:    &http.Client{Timeout: 2 * time.Second},
	}
}

// A non-200 from trust-safety must be an error: decoding it would yield an
// empty keyword list, which is indistinguishable from "this user filters
// nothing" — the exact defect M2-P0-3 removed from the block lookup.
func TestGetFilterKeywords_NonOKStatusIsAnError(t *testing.T) {
	for _, status := range []int{
		http.StatusUnauthorized,
		http.StatusInternalServerError,
		http.StatusBadGateway,
	} {
		trust := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{}`))
		}))

		s := newFeedServiceWithTrust(trust.URL)
		_, err := s.getFilterKeywords(context.Background(), uuid.New())
		trust.Close()

		if err == nil {
			t.Errorf("status %d returned no error — the feed would serve unfiltered", status)
		}
	}
}

func TestGetFilterKeywords_UnreachableIsAnError(t *testing.T) {
	s := newFeedServiceWithTrust("http://127.0.0.1:1")
	if _, err := s.getFilterKeywords(context.Background(), uuid.New()); err == nil {
		t.Fatal("an unreachable trust-safety must be an error")
	}
}

func TestGetFilterKeywords_MalformedBodyIsAnError(t *testing.T) {
	trust := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`this is not json`))
	}))
	defer trust.Close()

	s := newFeedServiceWithTrust(trust.URL)
	if _, err := s.getFilterKeywords(context.Background(), uuid.New()); err == nil {
		t.Fatal("a malformed keyword list must be an error, not an empty set")
	}
}

// applyKeywordHideFilter must propagate the lookup failure — this is the
// call HydratePosts makes, so this is the fail-closed guarantee the
// handlers rely on.
func TestApplyKeywordHideFilter_FailsClosed(t *testing.T) {
	s := newFeedServiceWithTrust("http://127.0.0.1:1")
	posts := []HydratedPost{hydratedWithText("anything")}
	if _, err := s.applyKeywordHideFilter(context.Background(), uuid.New(), posts); err == nil {
		t.Fatal("a failed keyword lookup must fail the page, not serve it unfiltered")
	}
}

// The happy path parses, and the empty set — the common case — is served
// from cache: the second call within the TTL must not hit trust-safety.
func TestGetFilterKeywords_EmptySetIsCached(t *testing.T) {
	var calls int32
	trust := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"keywords":[]}}`))
	}))
	defer trust.Close()

	s := newFeedServiceWithTrust(trust.URL)
	viewer := uuid.New()

	for i := 0; i < 3; i++ {
		kws, err := s.getFilterKeywords(context.Background(), viewer)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(kws) != 0 {
			t.Fatalf("expected empty set, got %v", kws)
		}
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("trust-safety was called %d times for 3 lookups — the 60s cache is not working", n)
	}
}

func TestGetFilterKeywords_ParsesKeywords(t *testing.T) {
	trust := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"keywords":["spoilers","game of thrones"]}}`))
	}))
	defer trust.Close()

	s := newFeedServiceWithTrust(trust.URL)
	kws, err := s.getFilterKeywords(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(kws) != 2 || kws[0] != "spoilers" || kws[1] != "game of thrones" {
		t.Fatalf("kws = %v", kws)
	}
}

// Errors must NOT be cached: after a failure, a subsequent request with a
// healthy trust-safety succeeds instead of serving a poisoned minute.
func TestGetFilterKeywords_ErrorIsNotCached(t *testing.T) {
	var healthy atomic.Bool
	trust := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthy.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"keywords":["spoilers"]}}`))
	}))
	defer trust.Close()

	s := newFeedServiceWithTrust(trust.URL)
	viewer := uuid.New()

	if _, err := s.getFilterKeywords(context.Background(), viewer); err == nil {
		t.Fatal("expected the unhealthy lookup to error")
	}
	healthy.Store(true)
	kws, err := s.getFilterKeywords(context.Background(), viewer)
	if err != nil {
		t.Fatalf("recovered lookup errored: %v", err)
	}
	if len(kws) != 1 || kws[0] != "spoilers" {
		t.Fatalf("kws = %v, want [spoilers]", kws)
	}
}

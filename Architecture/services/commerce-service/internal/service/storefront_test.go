package service

// The storefront's rules that can be proved without a database: what a
// gallery submission is allowed to be, which asset represents a product,
// which sections reach the client, and how many times one response is
// allowed to call media-service.
//
// The last one is the reason this file uses a real *media.Client against an
// httptest server that COUNTS requests rather than a hand-written stub of the
// resolver. A stub of our own interface would prove that our code calls our
// interface once; a counting server proves that media-service receives one
// HTTP request for a page of twelve products, which is the property that
// actually keeps the home screen off the N+1 path.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/atpost/commerce-service/internal/media"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ─── The gallery's submission rules ─────────────────────────────────────

func TestAGalleryIsCappedAtEight(t *testing.T) {
	ids := make([]uuid.UUID, 9)
	for i := range ids {
		ids[i] = uuid.New()
	}
	if _, err := normaliseMediaIDs(ids); err != ErrTooManyMedia {
		t.Fatalf("err = %v, want ErrTooManyMedia — nine images on a product is a "+
			"detail page that never finishes loading", err)
	}
	// Exactly eight is allowed. An off-by-one here is a seller told their
	// eighth photograph is one too many.
	if _, err := normaliseMediaIDs(ids[:8]); err != nil {
		t.Fatalf("eight images refused: %v", err)
	}
}

// Order is the contract: the first id is the cover, and the rest are the
// carousel in the order the seller dragged them into.
func TestTheSubmittedOrderIsPreservedExactly(t *testing.T) {
	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	got, err := normaliseMediaIDs(ids)
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	for i := range ids {
		if got[i] != ids[i] {
			t.Fatalf("position %d = %s, want %s — the gallery was reordered on the "+
				"way in, so the cover is not the image the seller chose", i, got[i], ids[i])
		}
	}
}

// A duplicate is REFUSED, not silently dropped.
//
// De-duplicating would renumber everything after the duplicate, so a client
// that accidentally sent the same id twice would get a gallery in an order it
// never asked for and no indication anything happened.
func TestTheSameAssetTwiceIsRefusedRatherThanDeduplicated(t *testing.T) {
	dup := uuid.New()
	if _, err := normaliseMediaIDs([]uuid.UUID{uuid.New(), dup, dup}); err != ErrDuplicateMedia {
		t.Fatalf("err = %v, want ErrDuplicateMedia", err)
	}
}

func TestAnEmptyGallerySubmissionIsRefused(t *testing.T) {
	for name, ids := range map[string][]uuid.UUID{
		"nil":         nil,
		"empty":       {},
		"only zeroes": {uuid.Nil, uuid.Nil},
	} {
		if _, err := normaliseMediaIDs(ids); err != ErrNoMedia {
			t.Fatalf("%s: err = %v, want ErrNoMedia — a route whose job is to set "+
				"the gallery must not be able to blank it by accident", name, err)
		}
	}
}

// ─── Which asset represents a product ───────────────────────────────────

// The defect this closes: a seller attaches eight photographs through the
// gallery, `primary_image_media_id` stays NULL because the gallery editor
// never writes it, hydration finds no id, and the product renders as a grey
// box with a full gallery behind it.
func TestTheGalleryCoverStandsInWhenTheLegacyColumnIsEmpty(t *testing.T) {
	cover := uuid.New()
	p := &postgres.Product{CoverMediaID: &cover}
	if got := productMediaID(p); got == nil || *got != cover {
		t.Fatalf("productMediaID = %v, want the gallery cover %s", got, cover)
	}

	// When both exist the explicit primary wins: a seller who set it meant it.
	primary := uuid.New()
	p.PrimaryImageMediaID = &primary
	if got := productMediaID(p); got == nil || *got != primary {
		t.Fatalf("productMediaID = %v, want the primary %s", got, primary)
	}

	if productMediaID(&postgres.Product{}) != nil {
		t.Fatal("a product with no media resolved to an id")
	}
	if productMediaID(nil) != nil {
		t.Fatal("a nil product resolved to an id")
	}
}

// ─── Section omission ───────────────────────────────────────────────────

// An empty section is a heading with a blank strip under it, which on a phone
// reads as a failed load. It is dropped, never sent empty.
func TestASectionWithNoProductsIsOmittedNotSentEmpty(t *testing.T) {
	one := []*postgres.Product{{ID: uuid.New()}}
	got := buildHomeSections([]HomeSection{
		{Key: "deals", Products: nil},
		{Key: "best_sellers", Products: one},
		{Key: "new_arrivals", Products: []*postgres.Product{}},
	})
	if len(got) != 1 || got[0].Key != "best_sellers" {
		t.Fatalf("sections = %+v, want only best_sellers", got)
	}
	// And the result is a non-nil slice, so the JSON is `[]` and not `null`
	// — a client decoding into an array type fails on null.
	if buildHomeSections(nil) == nil {
		t.Fatal("buildHomeSections(nil) is nil; the home page would serialise sections as null")
	}
}

// ─── One media batch per response ───────────────────────────────────────

// countingMedia is media-service, answering every id with one rendition and
// counting how many HTTP requests it received.
func countingMedia(t *testing.T, calls *int64) *media.Client {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(calls, 1)
		var req struct {
			IDs []string `json:"ids"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		data := map[string]any{}
		for _, id := range req.IDs {
			data[id] = map[string]any{"variants": map[string]string{
				"thumb_150":   "https://cdn/" + id + "/t.jpg",
				"medium_1080": "https://cdn/" + id + "/m.jpg",
			}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(s.Close)
	return media.New(s.URL, "k")
}

func productsWithImages(n int) []*postgres.Product {
	out := make([]*postgres.Product, 0, n)
	for i := 0; i < n; i++ {
		id := uuid.New()
		out = append(out, &postgres.Product{ID: uuid.New(), PrimaryImageMediaID: &id})
	}
	return out
}

// A page of products costs ONE call to media-service, not one per product.
//
// Twenty sequential HTTP calls would add more latency than the query they
// decorate, which is the whole reason resolution is batched.
func TestAPageOfProductsResolvesInOneMediaCall(t *testing.T) {
	var calls int64
	s := &Service{media: countingMedia(t, &calls)}

	products := productsWithImages(12)
	s.hydrateProductImages(context.Background(), products)

	if calls != 1 {
		t.Fatalf("media-service received %d requests for one page; want 1", calls)
	}
	for i, p := range products {
		if p.ImageURL == "" || p.ThumbnailURL == "" {
			t.Fatalf("product %d has no resolved URLs", i)
		}
		if p.ImageURL == p.ThumbnailURL {
			t.Fatalf("product %d: the grid thumbnail and the display image are the "+
				"same URL — the rendition names did not match media-service's", i)
		}
	}
}

// The home page resolves EVERY section AND every banner in one call.
//
// Three sections hydrating independently is three sequential round trips on
// the app's opening screen; and because a discounted best seller appears in
// two sections, per-section batching also asks for the same id twice.
func TestTheWholeHomePageResolvesInOneMediaCall(t *testing.T) {
	var calls int64
	s := &Service{media: countingMedia(t, &calls)}

	shared := productsWithImages(4)
	bannerMedia := uuid.New()
	page := &HomePage{
		Banners: []*postgres.Banner{{ID: uuid.New(), ImageMediaID: &bannerMedia}},
		Sections: []HomeSection{
			{Key: "deals", Products: shared},
			{Key: "best_sellers", Products: shared}, // the same products again
			{Key: "new_arrivals", Products: productsWithImages(4)},
		},
	}
	s.hydrateHome(context.Background(), uuid.Nil, page)

	if calls != 1 {
		t.Fatalf("media-service received %d requests for one home page; want 1", calls)
	}
	if page.Banners[0].ImageURL == "" {
		t.Fatal("the banner did not resolve; the home rail draws empty cards")
	}
	for _, sec := range page.Sections {
		for i, p := range sec.Products {
			if p.ImageURL == "" {
				t.Fatalf("%s[%d] has no image URL", sec.Key, i)
			}
		}
	}
}

// A product with no media asks for nothing, rather than asking for uuid.Nil.
func TestAProductWithNoMediaCostsNoMediaCall(t *testing.T) {
	var calls int64
	s := &Service{media: countingMedia(t, &calls)}
	s.hydrateProductImages(context.Background(), []*postgres.Product{{ID: uuid.New()}, nil})
	if calls != 0 {
		t.Fatalf("media-service received %d requests for a page with no images; want 0", calls)
	}
}

// Hydration fails SOFT: media-service being down leaves the URLs empty and
// the catalogue renderable, rather than failing the browse request. The
// opposite of the write path, deliberately — see internal/media.
func TestAMediaOutageLeavesTheCatalogueRenderable(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer down.Close()
	s := &Service{media: media.New(down.URL, "k")}

	products := productsWithImages(3)
	s.hydrateProductImages(context.Background(), products)
	for _, p := range products {
		if p.ImageURL != "" {
			t.Fatal("a URL was invented while media-service was down")
		}
	}
}

// ─── Banner validation ──────────────────────────────────────────────────

func TestABannerMustBeOpenable(t *testing.T) {
	valid := uuid.NewString()
	cases := map[string]struct {
		in   BannerInput
		want bool // valid?
	}{
		"category with a uuid":     {BannerInput{Title: "T", TargetType: "category", TargetID: valid}, true},
		"search with query text":   {BannerInput{Title: "T", TargetType: "search", TargetID: "running shoes"}, true},
		"category with query text": {BannerInput{Title: "T", TargetType: "category", TargetID: "running shoes"}, false},
		"search with nothing":      {BannerInput{Title: "T", TargetType: "search", TargetID: "  "}, false},
		"unknown target type":      {BannerInput{Title: "T", TargetType: "page", TargetID: valid}, false},
		"no title to render":       {BannerInput{Title: " ", TargetType: "category", TargetID: valid}, false},
	}
	for name, tc := range cases {
		err := tc.in.validate()
		if tc.want && err != nil {
			t.Errorf("%s: refused a valid banner: %v", name, err)
		}
		if !tc.want && err == nil {
			t.Errorf("%s: accepted a banner the client cannot open", name)
		}
	}
}

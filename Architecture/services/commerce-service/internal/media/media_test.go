package media

// Whether a media id a client hands to commerce actually belongs to them.
//
// Twelve columns in commerce-service store a media id that arrived in a
// request body. Nothing checked any of them. The consequences ranged from a
// seller using a competitor's product photography to a seller attaching
// another person's PAN card as their own KYC evidence.
//
// media-service is stubbed here, deliberately. What is under test is the
// policy this client applies to media-service's answer — which fields must
// hold, and what happens when the answer does not arrive at all.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// stubMedia stands in for media-service, answering with whatever asset the
// test hands it, in the shared API envelope media-service really uses.
func stubMedia(t *testing.T, asset *Asset, status int) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"code":"NOT_FOUND"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": asset})
	}))
	t.Cleanup(s.Close)
	return s
}

func readyImage(owner uuid.UUID) *Asset {
	return &Asset{
		ID: uuid.New(), UploaderID: owner, FileType: "image",
		ProcessingStatus: "ready", ModerationStatus: "passed",
	}
}

// The defect: a seller attaches an asset somebody else uploaded.
func TestMediaUploadedByAnotherAccountIsRefused(t *testing.T) {
	victim, attacker := uuid.New(), uuid.New()
	asset := readyImage(victim)
	c := New(stubMedia(t, asset, http.StatusOK).URL, "k")

	err := c.VerifyOwned(context.Background(), asset.ID, attacker, KindImage)
	if !errors.Is(err, ErrNotYourMedia) {
		t.Fatalf("err = %v, want ErrNotYourMedia — a seller just attached another person's "+
			"upload, which on the seller_documents path is somebody else's PAN card", err)
	}
}

// And the refusal must not say who the real owner is.
func TestTheRefusalIsNotAnOwnershipOracle(t *testing.T) {
	victim, attacker := uuid.New(), uuid.New()
	asset := readyImage(victim)
	c := New(stubMedia(t, asset, http.StatusOK).URL, "k")

	err := c.VerifyOwned(context.Background(), asset.ID, attacker, KindImage)
	if strings.Contains(err.Error(), victim.String()) {
		t.Fatalf("the refusal named the real uploader: %v", err)
	}
}

func TestOwnReadyModeratedMediaIsAccepted(t *testing.T) {
	owner := uuid.New()
	asset := readyImage(owner)
	c := New(stubMedia(t, asset, http.StatusOK).URL, "k")

	if err := c.VerifyOwned(context.Background(), asset.ID, owner, KindImage); err != nil {
		t.Fatalf("a seller cannot use their own ready, moderated image: %v", err)
	}
}

// Each of the four conditions is checked. A partial check is how a rejected
// asset ends up on a live listing.
func TestEveryConditionIsEnforced(t *testing.T) {
	owner := uuid.New()
	cases := []struct {
		name  string
		mutit func(*Asset)
		want  error
	}{
		{"still uploading", func(a *Asset) { a.ProcessingStatus = "pending_upload" }, ErrMediaNotReady},
		{"still processing", func(a *Asset) { a.ProcessingStatus = "processing" }, ErrMediaNotReady},
		{"processing failed", func(a *Asset) { a.ProcessingStatus = "failed" }, ErrMediaNotReady},
		{"moderation pending", func(a *Asset) { a.ModerationStatus = "pending" }, ErrMediaNotPassed},
		{"moderation rejected", func(a *Asset) { a.ModerationStatus = "rejected" }, ErrMediaNotPassed},
		{"a video where an image is required", func(a *Asset) { a.FileType = "video" }, ErrMediaWrongKind},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asset := readyImage(owner)
			tc.mutit(asset)
			c := New(stubMedia(t, asset, http.StatusOK).URL, "k")
			err := c.VerifyOwned(context.Background(), asset.ID, owner, KindImage)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// KindAny still enforces ownership, readiness and moderation — it relaxes only
// the file type.
func TestKindAnyStillEnforcesEverythingElse(t *testing.T) {
	owner, other := uuid.New(), uuid.New()
	ctx := context.Background()

	video := readyImage(owner)
	video.FileType = "video"
	if err := New(stubMedia(t, video, http.StatusOK).URL, "k").
		VerifyOwned(ctx, video.ID, owner, KindAny); err != nil {
		t.Fatalf("KindAny rejected a valid video: %v", err)
	}

	stolen := readyImage(other)
	if err := New(stubMedia(t, stolen, http.StatusOK).URL, "k").
		VerifyOwned(ctx, stolen.ID, owner, KindAny); !errors.Is(err, ErrNotYourMedia) {
		t.Fatalf("KindAny skipped the ownership check: %v", err)
	}
}

func TestAnUnknownMediaIdIsRefused(t *testing.T) {
	c := New(stubMedia(t, nil, http.StatusNotFound).URL, "k")
	err := c.VerifyOwned(context.Background(), uuid.New(), uuid.New(), KindImage)
	if !errors.Is(err, ErrMediaNotFound) {
		t.Fatalf("err = %v, want ErrMediaNotFound", err)
	}
}

// ─── Fail closed ───────────────────────────────────────────────────────

// A media-service that is down must not become a media-service that says yes.
func TestAnUnreachableMediaServiceFailsClosed(t *testing.T) {
	// A server that is created and immediately closed: the address refuses
	// connections, which is what a down service looks like.
	s := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := s.URL
	s.Close()

	err := New(url, "k").VerifyOwned(context.Background(), uuid.New(), uuid.New(), KindImage)
	if !errors.Is(err, ErrMediaUnavailable) {
		t.Fatalf("err = %v, want ErrMediaUnavailable — an outage must not verify anything", err)
	}
	// And it must NOT be reported as the caller's fault: the edge maps
	// ErrMediaUnavailable to 503 and everything else to 4xx.
	for _, refusal := range []error{ErrNotYourMedia, ErrMediaNotFound, ErrMediaNotReady, ErrMediaNotPassed} {
		if errors.Is(err, refusal) {
			t.Fatalf("an outage was reported as %v — the seller is told they did something wrong", refusal)
		}
	}
}

func TestAnUpstreamErrorIsUnavailableNotApproval(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusForbidden} {
		c := New(stubMedia(t, nil, status).URL, "k")
		err := c.VerifyOwned(context.Background(), uuid.New(), uuid.New(), KindImage)
		if err == nil {
			t.Fatalf("status %d verified the media", status)
		}
		if !errors.Is(err, ErrMediaUnavailable) {
			t.Fatalf("status %d gave %v, want ErrMediaUnavailable", status, err)
		}
	}
}

// A 200 whose body has no asset in it must not count as verification. This is
// what a media-service response-shape change looks like from here, and it must
// break loudly rather than silently disabling the check.
func TestAnEmptyBodyDoesNotVerify(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer s.Close()

	err := New(s.URL, "k").VerifyOwned(context.Background(), uuid.New(), uuid.New(), KindImage)
	if !errors.Is(err, ErrMediaUnavailable) {
		t.Fatalf("err = %v; an upstream shape change silently disabled the ownership check", err)
	}
}

// ─── Batch ─────────────────────────────────────────────────────────────

// nil and zero ids are skipped: an optional media field left unset is not a
// violation.
func TestUnsetMediaFieldsAreSkipped(t *testing.T) {
	owner := uuid.New()
	asset := readyImage(owner)
	c := New(stubMedia(t, asset, http.StatusOK).URL, "k")

	var nilID *uuid.UUID
	zero := uuid.Nil
	if err := c.VerifyAllOwned(context.Background(), owner, KindImage, nilID, &zero); err != nil {
		t.Fatalf("an unset optional media field was treated as a violation: %v", err)
	}
}

// One bad id in a batch fails the batch.
func TestOneStolenIdFailsTheWholeBatch(t *testing.T) {
	owner := uuid.New()
	stolen := readyImage(uuid.New())
	c := New(stubMedia(t, stolen, http.StatusOK).URL, "k")

	id := stolen.ID
	if err := c.VerifyAllOwned(context.Background(), owner, KindImage, &id); !errors.Is(err, ErrNotYourMedia) {
		t.Fatalf("err = %v, want ErrNotYourMedia", err)
	}
}

// An unconfigured base URL yields a nil client, so cmd/server can make the
// environment decision in one place rather than at every call site.
func TestABlankURLYieldsNoClient(t *testing.T) {
	for _, u := range []string{"", "   "} {
		if New(u, "k") != nil {
			t.Fatalf("New(%q) returned a client", u)
		}
	}
}

// The internal service key is sent, so media-service can tell this apart from
// an anonymous caller.
func TestTheInternalKeyIsSent(t *testing.T) {
	owner := uuid.New()
	asset := readyImage(owner)
	var seen string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("X-Internal-Service-Key")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": asset})
	}))
	defer s.Close()

	if err := New(s.URL, "the-key").VerifyOwned(context.Background(), asset.ID, owner, KindImage); err != nil {
		t.Fatal(err)
	}
	if seen != "the-key" {
		t.Fatalf("X-Internal-Service-Key = %q, want the-key", seen)
	}
}

// ─── Resolving ids into URLs ───────────────────────────────────────────
//
// Commerce hands clients a bare media UUID, so no product screen can draw an
// image. Resolving server-side fixes it for every client at once.

func batchStub(t *testing.T, byID map[uuid.UUID]map[string]any, calls *int, status int) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls != nil {
			*calls++
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		var req struct {
			IDs []string `json:"ids"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if len(req.IDs) > BatchLimit {
			// media-service rejects the whole request over its cap.
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		data := map[string]any{}
		for _, idStr := range req.IDs {
			id, err := uuid.Parse(idStr)
			if err != nil {
				continue
			}
			if v, ok := byID[id]; ok {
				data[idStr] = v
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(s.Close)
	return s
}

// The rendition names below are media-service's REAL ones — `thumb_150`,
// `small_480`, `medium_1080` (internal/processing/image.go) — and that is the
// whole point of this test.
//
// It used to stub "thumbnail", "medium" and "small", names media-service has
// never produced, and it passed: URL() fell through every preference to
// `original` and Thumbnail() fell back to URL(). So a green test sat on top
// of a product grid where every tile downloaded the full-resolution camera
// upload. A fake that invents its upstream's vocabulary cannot fail.
func TestAMediaIdResolvesToADisplayURL(t *testing.T) {
	id := uuid.New()
	c := New(batchStub(t, map[uuid.UUID]map[string]any{
		id: {"variants": map[string]string{
			"thumb_150": "https://cdn/t.jpg", "medium_1080": "https://cdn/m.jpg", "original": "https://cdn/o.jpg",
		}},
	}, nil, http.StatusOK).URL, "k")

	got := c.ResolveURLs(context.Background(), []uuid.UUID{id})
	r, ok := got[id]
	if !ok {
		t.Fatal("the id did not resolve; every product screen shows a placeholder")
	}
	if r.URL() != "https://cdn/m.jpg" {
		t.Fatalf("URL() = %q, want the medium_1080 variant", r.URL())
	}
	if r.Thumbnail() != "https://cdn/t.jpg" {
		t.Fatalf("Thumbnail() = %q, want the thumb_150 variant", r.Thumbnail())
	}
}

// `original` is the full-resolution upload. Serving it to a grid of twenty is
// how a phone downloads forty megabytes to draw thumbnails.
func TestTheOriginalIsTheLastResortNotTheFirstChoice(t *testing.T) {
	id := uuid.New()
	c := New(batchStub(t, map[uuid.UUID]map[string]any{
		id: {"variants": map[string]string{"original": "https://cdn/huge.jpg", "small_480": "https://cdn/s.jpg"}},
	}, nil, http.StatusOK).URL, "k")

	r := c.ResolveURLs(context.Background(), []uuid.UUID{id})[id]
	if r.URL() == "https://cdn/huge.jpg" {
		t.Fatal("URL() chose the original over a smaller variant")
	}
	if r.Thumbnail() != "https://cdn/s.jpg" {
		t.Fatalf("Thumbnail() = %q", r.Thumbnail())
	}

	// But when the original is all there is, it is better than nothing.
	only := uuid.New()
	c2 := New(batchStub(t, map[uuid.UUID]map[string]any{
		only: {"variants": map[string]string{"original": "https://cdn/only.jpg"}},
	}, nil, http.StatusOK).URL, "k")
	if got := c2.ResolveURLs(context.Background(), []uuid.UUID{only})[only].URL(); got != "https://cdn/only.jpg" {
		t.Fatalf("URL() = %q with only an original available", got)
	}
}

// One batch for a page, not one call per product.
func TestAPageOfProductsCostsOneCall(t *testing.T) {
	byID := map[uuid.UUID]map[string]any{}
	ids := make([]uuid.UUID, 0, 20)
	for i := 0; i < 20; i++ {
		id := uuid.New()
		ids = append(ids, id)
		byID[id] = map[string]any{"variants": map[string]string{"medium": "https://cdn/x.jpg"}}
	}
	calls := 0
	c := New(batchStub(t, byID, &calls, http.StatusOK).URL, "k")

	got := c.ResolveURLs(context.Background(), ids)
	if len(got) != 20 {
		t.Fatalf("resolved %d of 20", len(got))
	}
	if calls != 1 {
		t.Fatalf("%d HTTP calls for one page — twenty sequential round trips would cost more "+
			"latency than the query they decorate", calls)
	}
}

// The same id twenty times is one lookup: a page of products from one seller
// shares a logo.
func TestRepeatedIdsAreAskedForOnce(t *testing.T) {
	id := uuid.New()
	calls := 0
	c := New(batchStub(t, map[uuid.UUID]map[string]any{
		id: {"variants": map[string]string{"medium": "https://cdn/x.jpg"}},
	}, &calls, http.StatusOK).URL, "k")

	ids := make([]uuid.UUID, 20)
	for i := range ids {
		ids[i] = id
	}
	if got := c.ResolveURLs(context.Background(), ids); len(got) != 1 {
		t.Fatalf("resolved %d entries for one distinct id", len(got))
	}
	if calls != 1 {
		t.Fatalf("%d calls", calls)
	}
}

// Over media-service's cap of 50 the request is chunked, not truncated. A
// silently dropped tail is a catalogue with images on the first fifty rows.
func TestMoreThanTheBatchLimitIsChunkedNotTruncated(t *testing.T) {
	byID := map[uuid.UUID]map[string]any{}
	ids := make([]uuid.UUID, 0, 120)
	for i := 0; i < 120; i++ {
		id := uuid.New()
		ids = append(ids, id)
		byID[id] = map[string]any{"variants": map[string]string{"medium": "https://cdn/x.jpg"}}
	}
	calls := 0
	c := New(batchStub(t, byID, &calls, http.StatusOK).URL, "k")

	got := c.ResolveURLs(context.Background(), ids)
	if len(got) != 120 {
		t.Fatalf("resolved %d of 120 — the tail was dropped", len(got))
	}
	if calls != 3 {
		t.Fatalf("%d calls for 120 ids at a limit of %d, want 3", calls, BatchLimit)
	}
}

// ─── Fail soft on the read path ────────────────────────────────────────

// The opposite rule from VerifyOwned, and deliberately so: a catalogue that
// will not load because the image service is down is worse than a catalogue of
// grey boxes. Nothing here decides authorisation or money.
func TestResolutionFailsSoft(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := dead.URL
	dead.Close()

	got := New(url, "k").ResolveURLs(context.Background(), []uuid.UUID{uuid.New()})
	if len(got) != 0 {
		t.Fatalf("got %d entries from a dead media-service", len(got))
	}
	// The contract is that it RETURNS, with nothing, rather than erroring —
	// there is no error to return, by design.
}

func TestAnUpstreamErrorOnResolutionIsSilent(t *testing.T) {
	c := New(batchStub(t, nil, nil, http.StatusInternalServerError).URL, "k")
	if got := c.ResolveURLs(context.Background(), []uuid.UUID{uuid.New()}); len(got) != 0 {
		t.Fatalf("got %d entries", len(got))
	}
}

func TestAnUnknownIdSimplyDoesNotResolve(t *testing.T) {
	known, unknown := uuid.New(), uuid.New()
	c := New(batchStub(t, map[uuid.UUID]map[string]any{
		known: {"variants": map[string]string{"medium": "https://cdn/x.jpg"}},
	}, nil, http.StatusOK).URL, "k")

	got := c.ResolveURLs(context.Background(), []uuid.UUID{known, unknown})
	if _, ok := got[unknown]; ok {
		t.Fatal("an id media-service does not know resolved to something")
	}
	if _, ok := got[known]; !ok {
		t.Fatal("one unknown id suppressed a known one")
	}
}

// A nil client is the unconfigured local case and must not panic.
func TestANilClientResolvesToNothing(t *testing.T) {
	var c *Client
	if got := c.ResolveURLs(context.Background(), []uuid.UUID{uuid.New()}); len(got) != 0 {
		t.Fatalf("got %d entries from a nil client", len(got))
	}
}

// Nil ids are skipped rather than asked about.
func TestNilIdsAreNotAskedFor(t *testing.T) {
	calls := 0
	c := New(batchStub(t, nil, &calls, http.StatusOK).URL, "k")
	c.ResolveURLs(context.Background(), []uuid.UUID{uuid.Nil, uuid.Nil})
	if calls != 0 {
		t.Fatalf("%d calls for nothing but nil ids", calls)
	}
}

// WHO IS LOOKING reaches media-service.
//
// The defect: ResolveURLs sent the internal service key and nothing else, so
// every batch arrived as the anonymous all-zeroes viewer and media-service's
// delivery gate refused every protected asset. The catalogue rendered as grey
// boxes and no layer reported an error — a denial is a valid answer.
func TestTheViewerIsForwardedToMediaService(t *testing.T) {
	var gotViewer string
	seen := make(chan struct{}, 1)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotViewer = r.Header.Get("X-User-Id")
		select {
		case seen <- struct{}{}:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer s.Close()

	c := New(s.URL, "k")
	viewer := uuid.New()
	c.ResolveURLs(WithViewer(context.Background(), viewer), []uuid.UUID{uuid.New()})
	<-seen
	if gotViewer != viewer.String() {
		t.Fatalf("media-service saw viewer %q, want %q — without it the gate refuses "+
			"every protected asset and the catalogue is grey boxes", gotViewer, viewer)
	}

	// Anonymous sends NO header at all, rather than the zero UUID: a product
	// page is public, and media-service's own anonymous handling is what
	// decides, not a viewer id that looks real and matches nobody.
	gotViewer = "not-set"
	c.ResolveURLs(context.Background(), []uuid.UUID{uuid.New()})
	<-seen
	if gotViewer != "" {
		t.Fatalf("an anonymous resolve sent X-User-Id=%q", gotViewer)
	}
}

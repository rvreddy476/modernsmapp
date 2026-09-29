package http

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// HEAD on the serve routes over httptest: the REAL handler, the REAL
// service.ServeHeads and a REAL delivery.Gate, with the store, the signer,
// the content authority and the object store faked. No PostgreSQL.
//
// The properties under test:
//
//   - a HEAD answers 200 from the rows — the rendition's type and length, no
//     body, no redirect — to exactly the audience the GET serves;
//   - a denial is byte-for-byte the missing answer, and the GET's;
//   - an anonymous-scoped asset, which the GET streams, is answered without
//     one read of the object store.

// ── fakes ───────────────────────────────────────────────────────────────

type headStore struct {
	media map[uuid.UUID]*postgres.MediaAsset
	fail  error
}

func (s *headStore) GetMediaWithVariants(_ context.Context, id uuid.UUID) (*postgres.MediaAsset, error) {
	if s.fail != nil {
		return nil, s.fail
	}
	m, ok := s.media[id]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	cp := *m
	return &cp, nil
}

// headAuthz stands in for the content authorities. It records every viewer
// it was asked about, as the gate handed it over (a signed-out viewer is the
// nil UUID rendered).
type headAuthz struct {
	allow   func(viewerID, mediaID string) bool
	err     error
	viewers []string
}

func (a *headAuthz) Authorize(_ context.Context, viewerID, mediaID string) error {
	a.viewers = append(a.viewers, viewerID)
	if a.err != nil {
		return a.err
	}
	if a.allow != nil && a.allow(viewerID, mediaID) {
		return nil
	}
	return delivery.ErrDeliveryDenied
}

// headSigner is the URL signer behind the gate. What it signs is discarded
// by a HEAD; the tests assert none of it reaches the response.
type headSigner struct{ public, signed []string }

func (s *headSigner) PublicURL(key string) (string, error) {
	s.public = append(s.public, key)
	return "https://cdn.example/" + key, nil
}

func (s *headSigner) SignProtected(key string, _ time.Duration, _ time.Time) (string, error) {
	s.signed = append(s.signed, key)
	return "https://signed.example/" + key + "?Signature=abc", nil
}

// headObjects is the object store. Every touch is counted: a HEAD must
// leave both counters at zero.
type headObjects struct {
	data         map[string][]byte
	stats, reads int
}

func (o *headObjects) stat(key string) (int64, bool) {
	o.stats++
	b, ok := o.data[key]
	return int64(len(b)), ok
}

func (o *headObjects) read(key string) []byte {
	o.reads++
	return o.data[key]
}

// headStreams stands at the seam GET /serve reads an anonymous asset's bytes
// through (Handler.streams), over the fake object store. It is the byte path
// only — it takes no audience decision — and the tests use it for two
// things: the positive control that the object store IS reached by a GET,
// and the GET's own answer for an id that does not exist.
type headStreams struct {
	store   *headStore
	objects *headObjects
	calls   int
}

func (s *headStreams) StreamAnonymous(ctx context.Context, _ uuid.UUID, mediaID uuid.UUID, variant, _ string) (*service.StreamResult, error) {
	s.calls++
	m, err := s.store.GetMediaWithVariants(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	if m.AccessScope != postgres.AccessScopeAnonymous {
		return nil, service.ErrNotAnonymousScope
	}
	key, mime := m.StorageKey, m.MimeType
	if variant != "original" {
		key = ""
		for _, v := range m.Variants {
			if v.Name == variant {
				key, mime = v.ObjectKey, v.Mime
			}
		}
		if key == "" {
			return nil, fmt.Errorf("variant %q not found", variant)
		}
	}
	size, ok := s.objects.stat(key)
	if !ok {
		return nil, fmt.Errorf("stat object %s", key)
	}
	return &service.StreamResult{Data: s.objects.read(key), ContentType: mime, Size: size, End: size - 1}, nil
}

// ── fixture ─────────────────────────────────────────────────────────────

const (
	headOriginalSize = int64(52_428_801)
	head720pSize     = int64(21_000_007)
	head480pSize     = int64(9_000_003)
	headThumb150Size = int64(4_321)
	headSmall480Size = int64(20_011)
)

type headFixture struct {
	router  *gin.Engine
	store   *headStore
	authz   *headAuthz
	signer  *headSigner
	objects *headObjects
	streams *headStreams

	owner, viewer, stranger uuid.UUID

	// publicID: a public post's video — the authority admits everyone,
	// signed-out included. privateID: the authority admits `viewer` and the
	// owner. anonID / datingID: the scoped assets. cdnID: keys under
	// public/, no authority at all. avatarID / bareAvatarID: an image with
	// and without the avatar renditions. unsizedID: rows that recorded no
	// length.
	publicID, privateID, anonID, datingID, cdnID, avatarID, bareAvatarID, unsizedID uuid.UUID
}

func sized(n int64) *int64 { return &n }

func newHeadFixture(t *testing.T) *headFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	f := &headFixture{
		owner: uuid.New(), viewer: uuid.New(), stranger: uuid.New(),
		publicID: uuid.New(), privateID: uuid.New(), anonID: uuid.New(), datingID: uuid.New(),
		cdnID: uuid.New(), avatarID: uuid.New(), bareAvatarID: uuid.New(), unsizedID: uuid.New(),
	}
	key := func(prefix string, id uuid.UUID, name string) string {
		return fmt.Sprintf("%s%s/%s/%s", prefix, f.owner, id, name)
	}
	// The original is a QuickTime upload and the renditions are MP4, so a
	// response that reported the wrong row's type or length is visible.
	video := func(id uuid.UUID, scope, prefix string) *postgres.MediaAsset {
		return &postgres.MediaAsset{
			ID: id, UploaderID: f.owner, FileType: "video",
			MimeType: "video/quicktime", FileSizeBytes: headOriginalSize,
			StorageBucket: "media", StorageKey: key(prefix, id, "original"),
			ProcessingStatus: "ready", ModerationStatus: "passed",
			HLSMasterKey: key(prefix, id, "hls/master.m3u8"),
			AccessScope:  scope,
			Variants: []postgres.MediaVariant{
				{MediaAssetID: id, Name: "480p", Mime: "video/mp4", SizeBytes: sized(head480pSize), ObjectKey: key(prefix, id, "480p.mp4")},
				{MediaAssetID: id, Name: "720p", Mime: "video/mp4", SizeBytes: sized(head720pSize), ObjectKey: key(prefix, id, "720p.mp4")},
			},
		}
	}
	image := func(id uuid.UUID, variants ...postgres.MediaVariant) *postgres.MediaAsset {
		return &postgres.MediaAsset{
			ID: id, UploaderID: f.owner, FileType: "image",
			MimeType: "image/png", FileSizeBytes: headOriginalSize,
			StorageBucket: "media", StorageKey: key("user/", id, "original"),
			ProcessingStatus: "ready", ModerationStatus: "passed",
			Variants: variants,
		}
	}
	unsized := video(f.unsizedID, "", "user/")
	unsized.FileSizeBytes = 0
	for i := range unsized.Variants {
		unsized.Variants[i].SizeBytes = nil
	}

	f.store = &headStore{media: map[uuid.UUID]*postgres.MediaAsset{
		f.publicID:  video(f.publicID, "", "user/"),
		f.privateID: video(f.privateID, "", "user/"),
		f.anonID:    video(f.anonID, postgres.AccessScopeAnonymous, "user/"),
		f.datingID:  video(f.datingID, postgres.AccessScopeDatingPhoto, "user/"),
		f.cdnID:     video(f.cdnID, "", delivery.PublicPrefix),
		f.avatarID: image(f.avatarID,
			postgres.MediaVariant{MediaAssetID: f.avatarID, Name: "small_480", Mime: "image/webp", SizeBytes: sized(headSmall480Size), ObjectKey: key("user/", f.avatarID, "small_480.webp")},
			postgres.MediaVariant{MediaAssetID: f.avatarID, Name: "thumb_150", Mime: "image/webp", SizeBytes: sized(headThumb150Size), ObjectKey: key("user/", f.avatarID, "thumb_150.webp")},
		),
		f.bareAvatarID: image(f.bareAvatarID),
		f.unsizedID:    unsized,
	}}
	f.authz = &headAuthz{allow: func(viewerID, mediaID string) bool {
		switch mediaID {
		case f.privateID.String():
			// post-service admits a post's author and its audience.
			return viewerID == f.viewer.String() || viewerID == f.owner.String()
		case f.anonID.String():
			// group-service admits the group's members; `viewer` is one.
			return viewerID == f.viewer.String()
		}
		return true // anyone, signed-out included
	}}
	f.signer = &headSigner{}
	f.objects = &headObjects{data: map[string][]byte{}}
	for _, m := range f.store.media {
		f.objects.data[m.StorageKey] = []byte("bytes of " + m.StorageKey)
		for _, v := range m.Variants {
			f.objects.data[v.ObjectKey] = []byte("bytes of " + v.ObjectKey)
		}
	}
	f.streams = &headStreams{store: f.store, objects: f.objects}
	f.router = f.build(delivery.NewGate(f.signer, f.authz))
	return f
}

// build wires the real handler over the fakes.
func (f *headFixture) build(gate *delivery.Gate) *gin.Engine {
	r := gin.New()
	h := &Handler{heads: service.NewServeHeads(f.store, gate), streams: f.streams}
	h.RegisterRoutes(r, passthrough, passthrough)
	return r
}

func (f *headFixture) do(method, path string, viewer uuid.UUID) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if viewer != uuid.Nil {
		req.Header.Set("X-User-Id", viewer.String())
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func servePath(id uuid.UUID, variant string) string {
	if variant == "" {
		return "/v1/media/" + id.String() + "/serve"
	}
	return "/v1/media/" + id.String() + "/serve/" + variant
}

func (f *headFixture) head(id uuid.UUID, variant string, viewer uuid.UUID) *httptest.ResponseRecorder {
	return f.do(http.MethodHead, servePath(id, variant), viewer)
}

// wantHead asserts the whole 200: the type and length of the named row, the
// serve headers, no body and no redirect.
func wantHead(t *testing.T, what string, rec *httptest.ResponseRecorder, contentType string, size int64) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: got %d %s want 200", what, rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Errorf("%s: a HEAD carried a %d-byte body: %q", what, rec.Body.Len(), rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("%s: a HEAD redirected to %q; the signed URL it would reach is for GET", what, loc)
	}
	want := map[string]string{
		"Content-Type":   contentType,
		"Content-Length": strconv.FormatInt(size, 10),
		"Accept-Ranges":  "bytes",
		"Cache-Control":  "private, max-age=60",
		"Vary":           "Cookie, Authorization, X-User-Id",
	}
	for name, value := range want {
		if got := rec.Header().Get(name); got != value {
			t.Errorf("%s: %s = %q want %q", what, name, got, value)
		}
	}
	wantNothingOfTheObject(t, what, rec)
}

// wantNothingOfTheObject: neither the discarded signed URL nor a storage key
// (which names the uploader) reaches the client, in a header or in a body.
func wantNothingOfTheObject(t *testing.T, what string, rec *httptest.ResponseRecorder) {
	t.Helper()
	seen := rec.Body.String()
	for name, values := range rec.Header() {
		seen += "\n" + name + ": " + strings.Join(values, ",")
	}
	for _, leak := range []string{"signed.example", "cdn.example", "Signature=", "user/", "original", ".mp4"} {
		if strings.Contains(seen, leak) {
			t.Errorf("%s: the response carries %q:\n%s", what, leak, seen)
		}
	}
}

// wantHeadDeniedLikeMissing asserts a resolved denial: 404 NOT_FOUND whose
// body AND headers are exactly what an id that does not exist gets.
func wantHeadDeniedLikeMissing(t *testing.T, what string, got, missing *httptest.ResponseRecorder) {
	t.Helper()
	if got.Code != http.StatusNotFound || errorCode(t, got) != "NOT_FOUND" {
		t.Fatalf("%s: got %d %s want 404 NOT_FOUND", what, got.Code, got.Body.String())
	}
	if missing.Code != http.StatusNotFound {
		t.Fatalf("%s: the missing-id control is %d, not 404", what, missing.Code)
	}
	if got.Body.String() != missing.Body.String() {
		t.Errorf("%s: denial body differs from the missing-asset body\n denied: %s\nmissing: %s", what, got.Body.String(), missing.Body.String())
	}
	if g, m := fmt.Sprint(got.Header()), fmt.Sprint(missing.Header()); g != m {
		t.Errorf("%s: denial headers differ from the missing-asset headers\n denied: %s\nmissing: %s", what, g, m)
	}
	for _, name := range []string{"Accept-Ranges", "Location"} {
		if v := got.Header().Get(name); v != "" {
			t.Errorf("%s: a denial carries %s: %q", what, name, v)
		}
	}
	for _, size := range []int64{headOriginalSize, head720pSize, head480pSize} {
		if got.Header().Get("Content-Length") == strconv.FormatInt(size, 10) {
			t.Errorf("%s: a denial reports the object's length %d", what, size)
		}
	}
	wantNothingOfTheObject(t, what, got)
}

// ── route inventory ─────────────────────────────────────────────────────

// gin does not answer HEAD from a GET route: without these two
// registrations a feed validator's HEAD is a bare 404.
func TestHeadServeRoutesAreRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(service.New(postgres.New(nil), nil)).RegisterRoutes(r, passthrough, passthrough)

	want := map[string]string{
		"HEAD /v1/media/:mediaId/serve":          ".HeadMedia",
		"HEAD /v1/media/:mediaId/serve/:variant": ".HeadMediaVariant",
		"GET /v1/media/:mediaId/serve":           ".ServeMedia",
		"GET /v1/media/:mediaId/serve/:variant":  ".ServeMediaVariant",
	}
	found := map[string]string{}
	for _, ri := range r.Routes() {
		found[ri.Method+" "+ri.Path] = ri.Handler
	}
	for key, handler := range want {
		h, ok := found[key]
		if !ok {
			t.Errorf("%s is not registered", key)
			continue
		}
		if !strings.HasSuffix(strings.TrimSuffix(h, "-fm"), handler) {
			t.Errorf("%s served by %s, want %s", key, h, handler)
		}
	}
}

// ── the 200 ─────────────────────────────────────────────────────────────

// The feed's enclosure: a signed-out caller, a public post's 720p.
func TestHeadPublicVariantAnswersTheVariantRow(t *testing.T) {
	f := newHeadFixture(t)
	rec := f.head(f.publicID, "720p", uuid.Nil)
	wantHead(t, "signed-out on a public 720p", rec, "video/mp4", head720pSize)

	if len(f.authz.viewers) != 1 || !delivery.AnonymousViewer(f.authz.viewers[0]) {
		t.Errorf("the authority was asked about %v; want once, about the signed-out viewer", f.authz.viewers)
	}
	if f.objects.stats != 0 || f.objects.reads != 0 {
		t.Errorf("a HEAD touched the object store: %d stats, %d reads", f.objects.stats, f.objects.reads)
	}
}

func TestHeadEachRenditionReportsItsOwnRow(t *testing.T) {
	f := newHeadFixture(t)
	wantHead(t, "480p", f.head(f.publicID, "480p", uuid.Nil), "video/mp4", head480pSize)
	wantHead(t, "/serve", f.head(f.publicID, "", uuid.Nil), "video/quicktime", headOriginalSize)
	wantHead(t, "/serve/original", f.head(f.publicID, "original", uuid.Nil), "video/quicktime", headOriginalSize)
}

// Over a real connection: what a podcast app's HTTP client sees.
func TestHeadOverTheWire(t *testing.T) {
	f := newHeadFixture(t)
	srv := httptest.NewServer(f.router)
	defer srv.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("a HEAD must not redirect")
	}}

	resp, err := client.Head(srv.URL + servePath(f.publicID, "720p"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.ContentLength != head720pSize || len(body) != 0 {
		t.Errorf("public 720p: got %d, Content-Length %d, %d body bytes; want 200, %d, 0",
			resp.StatusCode, resp.ContentLength, len(body), head720pSize)
	}
	if got := resp.Header.Get("Content-Type"); got != "video/mp4" {
		t.Errorf("Content-Type = %q want video/mp4", got)
	}

	resp, err = client.Head(srv.URL + servePath(f.privateID, "720p"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound || len(body) != 0 || resp.ContentLength == head720pSize {
		t.Errorf("private 720p signed-out: got %d, Content-Length %d, %d body bytes; want 404 with nothing of the object",
			resp.StatusCode, resp.ContentLength, len(body))
	}
}

// A key under public/ is served by the GET with no authority call; so is
// its HEAD.
func TestHeadPublicClassKeyAsksNoAuthority(t *testing.T) {
	f := newHeadFixture(t)
	f.authz.err = delivery.ErrDeliveryUnresolved
	rec := f.head(f.cdnID, "720p", uuid.Nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Length") != strconv.FormatInt(head720pSize, 10) {
		t.Fatalf("public-class key: got %d %s (Content-Length %q) want 200 with the row's length",
			rec.Code, rec.Body.String(), rec.Header().Get("Content-Length"))
	}
	if len(f.authz.viewers) != 0 {
		t.Errorf("the authority was asked %d times about a public-class object", len(f.authz.viewers))
	}
}

// A row that recorded no length: the type is still true, and an absent
// Content-Length is honest where a zero would not be.
func TestHeadUnknownLengthOmitsContentLength(t *testing.T) {
	f := newHeadFixture(t)
	for _, variant := range []string{"", "720p"} {
		rec := f.head(f.unsizedID, variant, uuid.Nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%q: got %d %s want 200", variant, rec.Code, rec.Body.String())
		}
		if got, ok := rec.Header()["Content-Length"]; ok {
			t.Errorf("%q: Content-Length = %v for an object whose length was never recorded", variant, got)
		}
		if rec.Header().Get("Content-Type") == "" || rec.Header().Get("Accept-Ranges") != "bytes" {
			t.Errorf("%q: headers %v", variant, rec.Header())
		}
	}
}

// ── denial ──────────────────────────────────────────────────────────────

// Neutering the gate call in service.ServeHeads.HeadForViewer fails this
// test and the next.
func TestHeadSignedOutOnPrivateIs404LikeMissing(t *testing.T) {
	f := newHeadFixture(t)
	for _, variant := range []string{"", "720p"} {
		wantHeadDeniedLikeMissing(t, "signed-out on private "+variant,
			f.head(f.privateID, variant, uuid.Nil), f.head(uuid.New(), variant, uuid.Nil))
	}
}

func TestHeadStrangerOnPrivateIs404LikeMissing(t *testing.T) {
	f := newHeadFixture(t)
	wantHeadDeniedLikeMissing(t, "stranger on private",
		f.head(f.privateID, "720p", f.stranger), f.head(uuid.New(), "720p", f.stranger))
}

// The HEAD's denial is the GET's: the body a GET gives for an id that does
// not exist, through the GET handler itself.
func TestHeadDenialIsTheGetsNotFound(t *testing.T) {
	f := newHeadFixture(t)
	getMissing := f.do(http.MethodGet, servePath(uuid.New(), "720p"), uuid.Nil)
	if getMissing.Code != http.StatusNotFound {
		t.Fatalf("GET on a missing id: %d %s", getMissing.Code, getMissing.Body.String())
	}
	for what, rec := range map[string]*httptest.ResponseRecorder{
		"denied":            f.head(f.privateID, "720p", uuid.Nil),
		"missing":           f.head(uuid.New(), "720p", uuid.Nil),
		"missing rendition": f.head(f.publicID, "1080p", uuid.Nil),
	} {
		if rec.Code != getMissing.Code || rec.Body.String() != getMissing.Body.String() {
			t.Errorf("HEAD %s: %d %s\nGET missing: %d %s", what, rec.Code, rec.Body.String(), getMissing.Code, getMissing.Body.String())
		}
	}
}

func TestHeadPermittedViewerOnPrivateIs200(t *testing.T) {
	f := newHeadFixture(t)
	wantHead(t, "permitted viewer", f.head(f.privateID, "720p", f.viewer), "video/mp4", head720pSize)
	if len(f.authz.viewers) != 1 || f.authz.viewers[0] != f.viewer.String() {
		t.Errorf("the authority was asked about %v want [%s]", f.authz.viewers, f.viewer)
	}
}

// ── the owner ───────────────────────────────────────────────────────────

// An ordinary asset's owner is the authority's to admit, on the GET and so
// here: the viewer that reaches it must be the owner's own id.
func TestHeadOwnerOfPrivateAssetIsAdmittedByTheAuthority(t *testing.T) {
	f := newHeadFixture(t)
	wantHead(t, "owner", f.head(f.privateID, "", f.owner), "video/quicktime", headOriginalSize)
	if len(f.authz.viewers) != 1 || f.authz.viewers[0] != f.owner.String() {
		t.Errorf("the authority was asked about %v want [%s]", f.authz.viewers, f.owner)
	}
}

// The uploader of an anonymous-scoped asset is settled locally. The
// authority is UNRESOLVED here, so the only way to a 200 is not asking it.
func TestHeadOwnerOfAnonymousAssetNeedsNoAuthority(t *testing.T) {
	f := newHeadFixture(t)
	f.authz.err = delivery.ErrDeliveryUnresolved
	wantHead(t, "anonymous asset, uploader", f.head(f.anonID, "720p", f.owner), "video/mp4", head720pSize)
	if len(f.authz.viewers) != 0 {
		t.Errorf("the authority was asked %d times for the uploader's own asset", len(f.authz.viewers))
	}
}

// A dating photo is its uploader's alone on every public read route, though
// the authority here would admit anyone.
func TestHeadDatingAssetIsTheUploadersAlone(t *testing.T) {
	f := newHeadFixture(t)
	wantHead(t, "dating, uploader", f.head(f.datingID, "720p", f.owner), "video/mp4", head720pSize)

	for name, viewer := range map[string]uuid.UUID{"stranger": f.stranger, "signed-out": uuid.Nil} {
		f.authz.viewers = nil
		wantHeadDeniedLikeMissing(t, "dating, "+name, f.head(f.datingID, "720p", viewer), f.head(uuid.New(), "720p", viewer))
		if len(f.authz.viewers) != 0 {
			t.Errorf("dating, %s: the authority was asked; the scope is settled before it", name)
		}
	}
}

// ── anonymous-scoped assets ─────────────────────────────────────────────

// The GET streams these through this service. The HEAD answers from the
// rows: the object store is not touched and nothing is signed. The GET that
// follows is the control — it does reach the object store, through the same
// router, so the zero above is not the fake being unplugged.
func TestHeadAnonymousAssetReadsNoObject(t *testing.T) {
	f := newHeadFixture(t)

	wantHead(t, "anonymous asset 720p", f.head(f.anonID, "720p", f.viewer), "video/mp4", head720pSize)
	wantHead(t, "anonymous asset original", f.head(f.anonID, "", f.viewer), "video/quicktime", headOriginalSize)
	if f.objects.stats != 0 || f.objects.reads != 0 {
		t.Errorf("a HEAD touched the object store: %d stats, %d reads", f.objects.stats, f.objects.reads)
	}
	if f.streams.calls != 0 {
		t.Errorf("a HEAD went down the GET's byte path %d times", f.streams.calls)
	}
	if len(f.signer.signed)+len(f.signer.public) != 0 {
		t.Errorf("a URL was signed for an anonymous asset: %v %v", f.signer.signed, f.signer.public)
	}

	get := f.do(http.MethodGet, servePath(f.anonID, "720p"), f.viewer)
	if get.Code != http.StatusOK || get.Body.Len() == 0 {
		t.Fatalf("control GET: %d with %d body bytes", get.Code, get.Body.Len())
	}
	if f.objects.reads != 1 {
		t.Errorf("control GET read the object %d times want 1", f.objects.reads)
	}
}

// Neutering authorizeCaptionRead in the anonymous branch fails this test.
func TestHeadAnonymousAssetDeniedLikeMissing(t *testing.T) {
	f := newHeadFixture(t)
	for name, viewer := range map[string]uuid.UUID{"non-member": f.stranger, "signed-out": uuid.Nil} {
		wantHeadDeniedLikeMissing(t, "anonymous asset, "+name,
			f.head(f.anonID, "720p", viewer), f.head(uuid.New(), "720p", viewer))
	}
	if f.objects.stats != 0 || f.objects.reads != 0 {
		t.Errorf("a denied HEAD touched the object store: %d stats, %d reads", f.objects.stats, f.objects.reads)
	}
}

// The GET of an anonymous asset looks every name up literally, so `avatar`
// is not an alias there; the HEAD must not report a rendition the GET
// would not deliver.
func TestHeadAnonymousAssetHasNoAvatarAlias(t *testing.T) {
	f := newHeadFixture(t)
	get := f.do(http.MethodGet, servePath(f.anonID, service.AvatarVariant), f.viewer)
	head := f.head(f.anonID, service.AvatarVariant, f.viewer)
	if get.Code != http.StatusNotFound {
		t.Fatalf("control GET /serve/avatar on an anonymous asset: %d", get.Code)
	}
	if head.Code != get.Code || head.Body.String() != get.Body.String() {
		t.Errorf("HEAD %d %s\n GET %d %s", head.Code, head.Body.String(), get.Code, get.Body.String())
	}
}

// ── unresolved ──────────────────────────────────────────────────────────

func TestHeadUnresolvedAuthorityIs503(t *testing.T) {
	f := newHeadFixture(t)
	f.authz.err = delivery.ErrDeliveryUnresolved
	for name, tc := range map[string]struct {
		id     uuid.UUID
		viewer uuid.UUID
	}{
		"signed-out on public":    {f.publicID, uuid.Nil},
		"viewer on private":       {f.privateID, f.viewer},
		"member on anonymous":     {f.anonID, f.viewer},
		"signed-out on anonymous": {f.anonID, uuid.Nil},
	} {
		rec := f.head(tc.id, "720p", tc.viewer)
		if rec.Code != http.StatusServiceUnavailable || errorCode(t, rec) != "DEPENDENCY_UNAVAILABLE" {
			t.Errorf("%s with the authority down: got %d %s want 503 DEPENDENCY_UNAVAILABLE", name, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Content-Length") == strconv.FormatInt(head720pSize, 10) {
			t.Errorf("%s: a 503 reports the object's length", name)
		}
	}
}

// A store fault is not "gone": feed validators cache a 404.
func TestHeadStoreFaultIs503(t *testing.T) {
	f := newHeadFixture(t)
	f.store.fail = errors.New("connection refused")
	rec := f.head(f.publicID, "720p", uuid.Nil)
	if rec.Code != http.StatusServiceUnavailable || errorCode(t, rec) != "DEPENDENCY_UNAVAILABLE" {
		t.Errorf("store down: got %d %s want 503 DEPENDENCY_UNAVAILABLE", rec.Code, rec.Body.String())
	}
}

// An unwired gate is not permissive.
func TestHeadWithoutAGateIs503(t *testing.T) {
	f := newHeadFixture(t)
	f.router = f.build(nil)
	for name, id := range map[string]uuid.UUID{"ordinary": f.publicID, "anonymous": f.anonID, "public-class": f.cdnID} {
		if rec := f.head(id, "720p", f.viewer); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s asset with no gate: got %d %s want 503", name, rec.Code, rec.Body.String())
		}
	}
}

// ── renditions without a row, and the avatar alias ──────────────────────

// The HLS master is rewritten per request and has no row, so there is no
// length to report: not-found, without asking anyone. A rendition the asset
// never had is the same answer.
func TestHeadHLSAndMissingRenditionsAre404(t *testing.T) {
	f := newHeadFixture(t)
	for _, variant := range []string{"hls", "1080p", "thumb_150"} {
		for name, id := range map[string]uuid.UUID{"ordinary": f.publicID, "anonymous": f.anonID} {
			f.authz.viewers = nil
			wantHeadDeniedLikeMissing(t, name+" "+variant,
				f.head(id, variant, f.viewer), f.head(uuid.New(), variant, f.viewer))
			if name == "ordinary" && len(f.authz.viewers) != 0 {
				t.Errorf("%s %s: the authority was asked about a rendition that does not exist", name, variant)
			}
		}
	}
	if f.objects.stats != 0 || f.objects.reads != 0 {
		t.Errorf("a HEAD touched the object store: %d stats, %d reads", f.objects.stats, f.objects.reads)
	}
}

// `avatar` resolves to the smallest adequate rendition the asset has, and to
// the original when it has none — and reports THAT row, not the alias.
func TestHeadAvatarReportsTheResolvedRendition(t *testing.T) {
	f := newHeadFixture(t)
	wantHead(t, "avatar with renditions", f.head(f.avatarID, service.AvatarVariant, uuid.Nil), "image/webp", headThumb150Size)
	if len(f.signer.signed) != 1 || !strings.HasSuffix(f.signer.signed[0], "thumb_150.webp") {
		t.Errorf("the gate was asked about %v; want the thumb_150 object the GET would redirect to", f.signer.signed)
	}
	wantHead(t, "avatar without renditions", f.head(f.bareAvatarID, service.AvatarVariant, uuid.Nil), "image/png", headOriginalSize)
}

func TestHeadAvatarIsGated(t *testing.T) {
	f := newHeadFixture(t)
	f.authz.allow = func(string, string) bool { return false }
	wantHeadDeniedLikeMissing(t, "avatar denied",
		f.head(f.avatarID, service.AvatarVariant, uuid.Nil), f.head(uuid.New(), service.AvatarVariant, uuid.Nil))
}

// ── input ───────────────────────────────────────────────────────────────

func TestHeadInvalidMediaIDIs400(t *testing.T) {
	f := newHeadFixture(t)
	for _, path := range []string{"/v1/media/not-a-uuid/serve", "/v1/media/not-a-uuid/serve/720p"} {
		head := f.do(http.MethodHead, path, uuid.Nil)
		get := f.do(http.MethodGet, path, uuid.Nil)
		if head.Code != http.StatusBadRequest || head.Body.String() != get.Body.String() {
			t.Errorf("%s: HEAD %d %s, GET %d %s", path, head.Code, head.Body.String(), get.Code, get.Body.String())
		}
	}
}

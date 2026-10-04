package http

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/media-service/internal/store/blob"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Seller KYC documents, view-only — GET /v1/media/internal/:mediaId/image-bytes
// over httptest, through the REAL RegisterRoutes and the REAL reader, with
// fake record and object stores. No database, no object store.

const imageBytesKey = "image-bytes-internal-key"
const imageBytesPNGSecret = "PNG-TEXT-SECRET-SELLER-HOME-ADDRESS"

type fakeImageRecords struct {
	assets   map[uuid.UUID]*postgres.MediaAsset
	variants map[uuid.UUID][]postgres.MediaVariant
}

func (s *fakeImageRecords) GetMedia(_ context.Context, id uuid.UUID) (*postgres.MediaAsset, error) {
	a, ok := s.assets[id]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	cp := *a
	return &cp, nil
}

func (s *fakeImageRecords) GetVariants(_ context.Context, id uuid.UUID) ([]postgres.MediaVariant, error) {
	return s.variants[id], nil
}

type fakeImageObjects struct {
	objects map[string][]byte
	broken  map[string]bool  // keys whose read fails with a transport error
	sizes   map[string]int64 // reported size, when it should differ from the bytes
	read    map[string]int   // bytes handed out per key
}

type countingReader struct {
	r io.Reader
	n *int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	*c.n += n
	return n, err
}

func (o *fakeImageObjects) OpenObject(_ context.Context, key string) (io.ReadCloser, blob.ObjectInfo, error) {
	if o.broken[key] {
		return nil, blob.ObjectInfo{}, errors.New("blob: connection reset")
	}
	d, ok := o.objects[key]
	if !ok {
		return nil, blob.ObjectInfo{}, blob.ErrObjectNotFound
	}
	size := int64(len(d))
	if s, ok := o.sizes[key]; ok {
		size = s
	}
	n := 0
	cr := &countingReader{r: bytes.NewReader(d), n: &n}
	return readCloser{Reader: cr, close: func() { o.read[key] += n }}, blob.ObjectInfo{Size: size}, nil
}

type readCloser struct {
	io.Reader
	close func()
}

func (r readCloser) Close() error { r.close(); return nil }

// fakeImageBytesSvc returns a fixed image, for the handler's own size guard.
type fakeImageBytesSvc struct{ img *processing.DisplayImage }

func (f fakeImageBytesSvc) DisplayImage(context.Context, uuid.UUID) (*processing.DisplayImage, error) {
	return f.img, nil
}

type imageBytesFixture struct {
	records *fakeImageRecords
	objects *fakeImageObjects
	reader  *service.ImageBytesReader

	commerceSigner, doorstepSigner, postSigner, rogueSigner *servicetoken.Signer
	verifier                                *servicetoken.Verifier

	photo, pngDoc, variantOnly, video, notReady, failed, deleted, rejected, gone, outage uuid.UUID
}

func mustSigner(t *testing.T, issuer, kid string) (*servicetoken.Signer, string) {
	t.Helper()
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	s, err := servicetoken.NewSignerFromBase64(issuer, kid, priv)
	if err != nil {
		t.Fatal(err)
	}
	return s, pub
}

// pngWithText is a 320x200 PNG carrying a tEXt chunk with imageBytesPNGSecret.
func pngWithText(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 320, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 320; x++ {
			img.Set(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 90, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	data := append([]byte("Comment\x00"), imageBytesPNGSecret...)
	chunk := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	typed := append([]byte("tEXt"), data...)
	chunk = append(chunk, typed...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(typed))
	// Signature (8) + IHDR chunk (4 len + 4 type + 13 data + 4 crc = 25).
	out := append([]byte{}, raw[:33]...)
	out = append(out, chunk...)
	out = append(out, raw[33:]...)
	if !bytes.Contains(out, []byte(imageBytesPNGSecret)) {
		t.Fatal("fixture: the PNG lost its text chunk")
	}
	if _, err := png.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("fixture: PNG with text chunk does not decode: %v", err)
	}
	return out
}

func newImageBytesFixture(t *testing.T) *imageBytesFixture {
	t.Helper()
	exifJPEG := datingJPEGWithComment(t, "COMMENT-SECRET-UPLOADER-DEVICE")
	if !processing.JPEGHasMetadata(exifJPEG) || !bytes.Contains(exifJPEG, []byte(datingGPSSecret)) {
		t.Fatal("fixture: the JPEG must carry EXIF with the GPS secret")
	}
	f := &imageBytesFixture{
		records: &fakeImageRecords{assets: map[uuid.UUID]*postgres.MediaAsset{}, variants: map[uuid.UUID][]postgres.MediaVariant{}},
		objects: &fakeImageObjects{objects: map[string][]byte{}, broken: map[string]bool{}, sizes: map[string]int64{}, read: map[string]int{}},
		photo:   uuid.New(), pngDoc: uuid.New(), variantOnly: uuid.New(), video: uuid.New(),
		notReady: uuid.New(), failed: uuid.New(), deleted: uuid.New(), rejected: uuid.New(),
		gone: uuid.New(), outage: uuid.New(),
	}
	add := func(id uuid.UUID, fileType, status, moderation string, body []byte) string {
		key := "user/" + uuid.NewString() + "/" + id.String() + "/original"
		f.records.assets[id] = &postgres.MediaAsset{ID: id, FileType: fileType, MimeType: "image/jpeg",
			ProcessingStatus: status, ModerationStatus: moderation, StorageKey: key}
		if body != nil {
			f.objects.objects[key] = body
		}
		return key
	}
	add(f.photo, "image", "ready", "passed", exifJPEG)
	add(f.pngDoc, "image", "ready", "passed", pngWithText(t))
	// Every refused asset holds perfectly servable image bytes, so only the
	// guard under test can be what refuses it.
	add(f.video, "video", "ready", "passed", exifJPEG)
	add(f.notReady, "image", "processing", "pending", exifJPEG)
	add(f.failed, "image", "failed", "pending", exifJPEG)
	add(f.deleted, "image", "deleted", "passed", exifJPEG)
	add(f.rejected, "image", "ready", "rejected", exifJPEG)
	add(f.gone, "image", "ready", "passed", nil)
	outageKey := add(f.outage, "image", "ready", "passed", exifJPEG)
	f.objects.broken[outageKey] = true

	// Original object gone; a medium_1080 rendition remains (here carrying
	// EXIF too, so the test proves the rendition is re-encoded as well).
	add(f.variantOnly, "image", "ready", "passed", nil)
	vkey := "user/x/" + f.variantOnly.String() + "/medium_1080"
	f.records.variants[f.variantOnly] = []postgres.MediaVariant{
		{MediaAssetID: f.variantOnly, Name: "thumb_150", ObjectKey: "missing-thumb", Mime: "image/jpeg"},
		{MediaAssetID: f.variantOnly, Name: "medium_1080", ObjectKey: vkey, Mime: "image/jpeg"},
	}
	f.objects.objects[vkey] = exifJPEG

	f.reader = service.NewImageBytesReader(f.records, f.objects)

	var commercePub, doorstepPub, postPub, roguePub string
	f.commerceSigner, commercePub = mustSigner(t, IssuerCommerceService, "c1")
	f.doorstepSigner, doorstepPub = mustSigner(t, IssuerDoorstepService, "doorstep-1")
	f.postSigner, postPub = mustSigner(t, "post-service", "p1")
	f.rogueSigner, roguePub = mustSigner(t, IssuerCommerceService, "c1") // same name, unregistered key
	_ = roguePub
	f.verifier = servicetoken.NewVerifier(AudienceMedia)
	if err := f.verifier.RegisterBase64(IssuerCommerceService, "c1", commercePub, []string{OpImageBytesRead}, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.verifier.RegisterBase64(IssuerDoorstepService, "doorstep-1", doorstepPub, []string{OpImageBytesRead}, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.verifier.RegisterBase64("post-service", "p1", postPub, []string{OpImageBytesRead}, nil); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *imageBytesFixture) router(t *testing.T, legacyKey bool) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(service.New(postgres.New(nil), nil)).WithInternalKey(imageBytesKey).WithImageBytesAuth(f.verifier, legacyKey)
	h.imageBytes = f.reader
	h.RegisterRoutes(r, passthrough, passthrough)
	return r
}

func mint(t *testing.T, s *servicetoken.Signer, audience string, scope ...string) string {
	t.Helper()
	tok, err := s.Mint(audience, "kyc-view", scope, nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func (f *imageBytesFixture) commerceToken(t *testing.T) string {
	return mint(t, f.commerceSigner, AudienceMedia, OpImageBytesRead)
}

func imageBytesPath(id string) string { return "/v1/media/internal/" + id + "/image-bytes" }

func getImageBytes(r *gin.Engine, path, key, token string, extra map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if key != "" {
		req.Header.Set("X-Internal-Service-Key", key)
	}
	if token != "" {
		req.Header.Set(ServiceAuthHeader, "Bearer "+token)
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func imageBytesErrorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("error body is not JSON: %q", w.Body.String())
	}
	return body.Error.Code
}

func assertNoStoreNoSniff(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if got := w.Header().Get("Cache-Control"); got != "no-store, private" {
		t.Fatalf("Cache-Control = %q, want %q", got, "no-store, private")
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
}

func TestImageBytes_RouteRegisteredOnlyInInternalGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	with := gin.New()
	New(service.New(postgres.New(nil), nil)).WithInternalKey("k").RegisterRoutes(with, passthrough, passthrough)
	found := false
	for _, ri := range with.Routes() {
		if ri.Method == http.MethodGet && ri.Path == "/v1/media/internal/:mediaId/image-bytes" {
			found = strings.Contains(ri.Handler, ".GetImageBytes")
		}
	}
	if !found {
		t.Fatal("GET /v1/media/internal/:mediaId/image-bytes is not registered to GetImageBytes")
	}
	without := gin.New()
	New(service.New(postgres.New(nil), nil)).RegisterRoutes(without, passthrough, passthrough)
	for _, ri := range without.Routes() {
		if strings.HasSuffix(ri.Path, "/image-bytes") {
			t.Fatalf("image-bytes must not exist without an internal key, found %s %s", ri.Method, ri.Path)
		}
	}
}

func TestImageBytes_ServesBytesAndHeaders(t *testing.T) {
	f := newImageBytesFixture(t)
	r := f.router(t, false)
	w := getImageBytes(r, imageBytesPath(f.photo.String()), imageBytesKey, f.commerceToken(t), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "image/jpeg" {
		t.Fatalf("Content-Type = %q, want image/jpeg", got)
	}
	assertNoStoreNoSniff(t, w)
	if w.Header().Get("Location") != "" || w.Header().Get("Content-Disposition") != "" {
		t.Fatal("image-bytes must stream bytes, never redirect or attach")
	}
	if cl := w.Header().Get("Content-Length"); cl == "" || cl == "0" {
		t.Fatalf("Content-Length = %q", cl)
	}
	img, format, err := image.Decode(bytes.NewReader(w.Body.Bytes()))
	if err != nil || format != "jpeg" {
		t.Fatalf("body is not a decodable JPEG: format %q, err %v", format, err)
	}
	// The fixture is 900x600 with EXIF orientation 6: served upright.
	if b := img.Bounds(); b.Dx() != 600 || b.Dy() != 900 {
		t.Fatalf("served %dx%d, want 600x900 (orientation applied)", b.Dx(), b.Dy())
	}

	// PNG stays PNG.
	w = getImageBytes(r, imageBytesPath(f.pngDoc.String()), imageBytesKey, f.commerceToken(t), nil)
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("png: status %d, Content-Type %q", w.Code, w.Header().Get("Content-Type"))
	}
	assertNoStoreNoSniff(t, w)
}

func TestImageBytes_NoEXIFReachesClient(t *testing.T) {
	f := newImageBytesFixture(t)
	r := f.router(t, false)
	for name, id := range map[string]uuid.UUID{"original": f.photo, "rendition fallback": f.variantOnly} {
		w := getImageBytes(r, imageBytesPath(id.String()), imageBytesKey, f.commerceToken(t), nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", name, w.Code, w.Body.String())
		}
		body := w.Body.Bytes()
		if processing.JPEGHasMetadata(body) {
			t.Fatalf("%s: served JPEG still carries an APPn/COM segment", name)
		}
		for _, leak := range []string{datingGPSSecret, "COMMENT-SECRET-UPLOADER-DEVICE", "Exif\x00\x00"} {
			if bytes.Contains(body, []byte(leak)) {
				t.Fatalf("%s: served bytes contain %q", name, leak)
			}
		}
	}
	w := getImageBytes(r, imageBytesPath(f.pngDoc.String()), imageBytesKey, f.commerceToken(t), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("png: status %d", w.Code)
	}
	if bytes.Contains(w.Body.Bytes(), []byte(imageBytesPNGSecret)) || bytes.Contains(w.Body.Bytes(), []byte("tEXt")) {
		t.Fatal("png: served bytes still carry the text chunk")
	}
}

func TestImageBytes_NotFoundSaysNothingMore(t *testing.T) {
	f := newImageBytesFixture(t)
	r := f.router(t, false)
	cases := map[string]string{
		"video":               f.video.String(),
		"not ready":           f.notReady.String(),
		"failed":              f.failed.String(),
		"deleted":             f.deleted.String(),
		"moderation rejected": f.rejected.String(),
		"missing row":         uuid.NewString(),
		"bytes gone":          f.gone.String(),
		"not a uuid":          "not-a-uuid",
	}
	var first string
	for name, id := range cases {
		w := getImageBytes(r, imageBytesPath(id), imageBytesKey, f.commerceToken(t), nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s: status %d, want 404: %s", name, w.Code, w.Body.String())
		}
		if code := imageBytesErrorCode(t, w); code != CodeMediaNotFound {
			t.Fatalf("%s: code %q, want %s", name, code, CodeMediaNotFound)
		}
		assertNoStoreNoSniff(t, w)
		// One answer for every reason: strip the request id, compare the rest.
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		delete(body, "meta")
		norm, _ := json.Marshal(body)
		if first == "" {
			first = string(norm)
		} else if string(norm) != first {
			t.Fatalf("%s: not-found body %s differs from %s", name, norm, first)
		}
	}
}

func TestImageBytes_StoreOutageIs503(t *testing.T) {
	f := newImageBytesFixture(t)
	w := getImageBytes(f.router(t, false), imageBytesPath(f.outage.String()), imageBytesKey, f.commerceToken(t), nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", w.Code)
	}
}

func TestImageBytes_RefusesNonCommerceServiceCaller(t *testing.T) {
	f := newImageBytesFixture(t)
	r := f.router(t, true) // even where the bare key is accepted, a token must be commerce's
	cases := map[string]string{
		"post-service token":           mint(t, f.postSigner, AudienceMedia, OpImageBytesRead),
		"commerce token, wrong scope":  mint(t, f.commerceSigner, AudienceMedia, "media:other.read"),
		"commerce token, wrong aud":    mint(t, f.commerceSigner, "commerce", OpImageBytesRead),
		"forged commerce-service name": mint(t, f.rogueSigner, AudienceMedia, OpImageBytesRead),
		"garbage":                      "not.a.token",
	}
	for name, tok := range cases {
		w := getImageBytes(r, imageBytesPath(f.photo.String()), imageBytesKey, tok, nil)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s: status %d, want 403", name, w.Code)
		}
		if code := imageBytesErrorCode(t, w); code != CodeServiceTokenRejected {
			t.Fatalf("%s: code %q", name, code)
		}
	}
	// No verifier configured: a token is never accepted.
	gin.SetMode(gin.TestMode)
	bare := gin.New()
	h := New(service.New(postgres.New(nil), nil)).WithInternalKey(imageBytesKey).WithImageBytesAuth(nil, true)
	h.imageBytes = f.reader
	h.RegisterRoutes(bare, passthrough, passthrough)
	if w := getImageBytes(bare, imageBytesPath(f.photo.String()), imageBytesKey, f.commerceToken(t), nil); w.Code != http.StatusForbidden {
		t.Fatalf("no verifier: status %d, want 403", w.Code)
	}
}

func TestImageBytes_LegacyKeyOnlyOnLocalDev(t *testing.T) {
	f := newImageBytesFixture(t)
	if w := getImageBytes(f.router(t, true), imageBytesPath(f.photo.String()), imageBytesKey, "", nil); w.Code != http.StatusOK {
		t.Fatalf("local/dev, bare key: status %d, want 200", w.Code)
	}
	w := getImageBytes(f.router(t, false), imageBytesPath(f.photo.String()), imageBytesKey, "", nil)
	if w.Code != http.StatusUnauthorized || imageBytesErrorCode(t, w) != CodeServiceTokenRequired {
		t.Fatalf("not local/dev, bare key: status %d, want 401 %s", w.Code, CodeServiceTokenRequired)
	}
}

func TestImageBytes_RequiresInternalKeyEvenWithToken(t *testing.T) {
	f := newImageBytesFixture(t)
	r := f.router(t, true)
	for _, key := range []string{"", "wrong-key"} {
		if w := getImageBytes(r, imageBytesPath(f.photo.String()), key, f.commerceToken(t), nil); w.Code != http.StatusUnauthorized {
			t.Fatalf("key %q: status %d, want 401", key, w.Code)
		}
	}
}

func TestImageBytes_RefusesUserIdentity(t *testing.T) {
	f := newImageBytesFixture(t)
	r := f.router(t, true)
	for _, hdr := range gatewayIdentityHeaders {
		w := getImageBytes(r, imageBytesPath(f.photo.String()), imageBytesKey, f.commerceToken(t), map[string]string{hdr: uuid.NewString()})
		if w.Code != http.StatusForbidden || imageBytesErrorCode(t, w) != CodeUserCallerRefused {
			t.Fatalf("%s: status %d, want 403 %s", hdr, w.Code, CodeUserCallerRefused)
		}
	}
}

func TestImageBytes_SizeCap(t *testing.T) {
	f := newImageBytesFixture(t)
	// The reader's render cap: nothing fits in 64 bytes, so the asset has no
	// servable image and answers not-found.
	f.reader.WithLimits(64, processing.DisplayImageMaxSourceBytes)
	if w := getImageBytes(f.router(t, false), imageBytesPath(f.photo.String()), imageBytesKey, f.commerceToken(t), nil); w.Code != http.StatusNotFound {
		t.Fatalf("render cap: status %d, want 404", w.Code)
	}
	// The source cap, honest size: an object the store says is too large is
	// refused before a byte of it is read.
	g := newImageBytesFixture(t)
	g.reader.WithLimits(processing.DisplayImageMaxBytes, 128)
	if w := getImageBytes(g.router(t, false), imageBytesPath(g.photo.String()), imageBytesKey, g.commerceToken(t), nil); w.Code != http.StatusNotFound {
		t.Fatalf("source cap: status %d, want 404", w.Code)
	}
	if n := g.objects.read[g.records.assets[g.photo].StorageKey]; n != 0 {
		t.Fatalf("source cap: %d bytes of an over-size object were read", n)
	}
	// The source cap, stale size: a small valid JPEG followed by junk, with a
	// reported size under the cap. The prefix the cap lets through decodes,
	// so only the length check stops a truncated read from being rendered.
	small := image.NewRGBA(image.Rect(0, 0, 16, 16))
	var sj bytes.Buffer
	if err := jpeg.Encode(&sj, small, &jpeg.Options{Quality: 50}); err != nil {
		t.Fatal(err)
	}
	k := newImageBytesFixture(t)
	key := k.records.assets[k.photo].StorageKey
	k.objects.objects[key] = append(append([]byte{}, sj.Bytes()...), make([]byte, 4096)...)
	k.objects.sizes[key] = int64(sj.Len())
	k.reader.WithLimits(processing.DisplayImageMaxBytes, int64(sj.Len()+100))
	if w := getImageBytes(k.router(t, false), imageBytesPath(k.photo.String()), imageBytesKey, k.commerceToken(t), nil); w.Code != http.StatusNotFound {
		t.Fatalf("stale-size source cap: status %d, want 404", w.Code)
	}
	// The handler's own 15 MB guard.
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(service.New(postgres.New(nil), nil)).WithInternalKey(imageBytesKey).WithImageBytesAuth(f.verifier, false)
	h.imageBytes = fakeImageBytesSvc{img: &processing.DisplayImage{Bytes: make([]byte, processing.DisplayImageMaxBytes+1), ContentType: "image/jpeg"}}
	h.RegisterRoutes(r, passthrough, passthrough)
	if w := getImageBytes(r, imageBytesPath(f.photo.String()), imageBytesKey, f.commerceToken(t), nil); w.Code != http.StatusNotFound {
		t.Fatalf("handler cap: status %d, want 404", w.Code)
	}
}

func TestServiceCallersFromEnv_Media(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if v, err := ServiceCallersFromEnv(env(nil)); v != nil || err != nil {
		t.Fatalf("blank SERVICE_CALLERS: %v, %v", v, err)
	}
	pub, _, _ := servicetoken.GenerateKeypair()
	v, err := ServiceCallersFromEnv(env(map[string]string{
		"SERVICE_CALLERS":                        "commerce-service",
		"SERVICE_CALLER_COMMERCE_SERVICE_KID":    "c1",
		"SERVICE_CALLER_COMMERCE_SERVICE_PUBKEY": pub,
		"SERVICE_CALLER_COMMERCE_SERVICE_OPS":    OpImageBytesRead,
	}))
	if err != nil || v == nil || v.Callers() != 1 {
		t.Fatalf("valid config: %v, %v", v, err)
	}
	if _, err := ServiceCallersFromEnv(env(map[string]string{
		"SERVICE_CALLERS":                        "commerce-service",
		"SERVICE_CALLER_COMMERCE_SERVICE_KID":    "c1",
		"SERVICE_CALLER_COMMERCE_SERVICE_PUBKEY": pub,
	})); err == nil {
		t.Fatal("a caller with no ops must be a configuration error")
	}
}

// Doorstep professional documents (4 Oct 2026): doorstep-service reads the
// same bytes commerce does, with the same one operation, and nothing else.

func TestImageBytes_DoorstepServiceReadsBytes(t *testing.T) {
	f := newImageBytesFixture(t)
	r := f.router(t, false)
	w := getImageBytes(r, imageBytesPath(f.photo.String()), imageBytesKey, mint(t, f.doorstepSigner, AudienceMedia, OpImageBytesRead), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("doorstep token: status %d, want 200: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "image/jpeg" {
		t.Fatalf("Content-Type = %q, want image/jpeg", got)
	}
	assertNoStoreNoSniff(t, w)
	if processing.JPEGHasMetadata(w.Body.Bytes()) || bytes.Contains(w.Body.Bytes(), []byte(datingGPSSecret)) {
		t.Fatal("doorstep: served JPEG still carries metadata")
	}
	// The commerce caller is unchanged.
	if w := getImageBytes(r, imageBytesPath(f.photo.String()), imageBytesKey, f.commerceToken(t), nil); w.Code != http.StatusOK {
		t.Fatalf("commerce token after doorstep was added: status %d, want 200", w.Code)
	}
}

func TestImageBytes_RefusesDoorstepTokenForAnotherOperationOrAudience(t *testing.T) {
	f := newImageBytesFixture(t)
	r := f.router(t, true) // even where the bare key is accepted
	cases := map[string]string{
		"doorstep token, recording import scope": mint(t, f.doorstepSigner, AudienceMedia, OpRecordingImport),
		"doorstep token, other scope":            mint(t, f.doorstepSigner, AudienceMedia, "media:other.read"),
		"doorstep token, payments audience":      mint(t, f.doorstepSigner, "payments", OpImageBytesRead),
		"doorstep token, doorstep audience":      mint(t, f.doorstepSigner, "doorstep", OpImageBytesRead),
	}
	for name, tok := range cases {
		w := getImageBytes(r, imageBytesPath(f.photo.String()), imageBytesKey, tok, nil)
		if w.Code != http.StatusForbidden || imageBytesErrorCode(t, w) != CodeServiceTokenRejected {
			t.Fatalf("%s: status %d, want 403 %s", name, w.Code, CodeServiceTokenRejected)
		}
	}
}

// Through the real env parsing: doorstep-service registered with its one
// operation reads bytes; registered for another operation only, it is refused
// even when its token claims image-bytes.
func TestImageBytes_DoorstepCallerFromEnv(t *testing.T) {
	f := newImageBytesFixture(t)
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := servicetoken.NewSignerFromBase64(IssuerDoorstepService, "doorstep-1", priv)
	if err != nil {
		t.Fatal(err)
	}
	commercePub, commercePriv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	commerce, err := servicetoken.NewSignerFromBase64(IssuerCommerceService, "c1", commercePriv)
	if err != nil {
		t.Fatal(err)
	}
	build := func(doorstepOps string) *gin.Engine {
		v, err := ServiceCallersFromEnv(func(k string) string {
			return map[string]string{
				"SERVICE_CALLERS":                        "commerce-service,doorstep-service",
				"SERVICE_CALLER_COMMERCE_SERVICE_KID":    "c1",
				"SERVICE_CALLER_COMMERCE_SERVICE_PUBKEY": commercePub,
				"SERVICE_CALLER_COMMERCE_SERVICE_OPS":    OpImageBytesRead,
				"SERVICE_CALLER_DOORSTEP_SERVICE_KID":    "doorstep-1",
				"SERVICE_CALLER_DOORSTEP_SERVICE_PUBKEY": pub,
				"SERVICE_CALLER_DOORSTEP_SERVICE_OPS":    doorstepOps,
			}[k]
		})
		if err != nil || v == nil || v.Callers() != 2 {
			t.Fatalf("env config: %v, %v", v, err)
		}
		gin.SetMode(gin.TestMode)
		r := gin.New()
		h := New(service.New(postgres.New(nil), nil)).WithInternalKey(imageBytesKey).WithImageBytesAuth(v, false)
		h.imageBytes = f.reader
		h.RegisterRoutes(r, passthrough, passthrough)
		return r
	}
	path := imageBytesPath(f.photo.String())
	ok := build(OpImageBytesRead)
	if w := getImageBytes(ok, path, imageBytesKey, mint(t, signer, AudienceMedia, OpImageBytesRead), nil); w.Code != http.StatusOK {
		t.Fatalf("doorstep with media:image-bytes.read: status %d, want 200", w.Code)
	}
	if w := getImageBytes(ok, path, imageBytesKey, mint(t, commerce, AudienceMedia, OpImageBytesRead), nil); w.Code != http.StatusOK {
		t.Fatalf("commerce alongside doorstep: status %d, want 200", w.Code)
	}
	other := build(OpRecordingImport)
	if w := getImageBytes(other, path, imageBytesKey, mint(t, signer, AudienceMedia, OpImageBytesRead), nil); w.Code != http.StatusForbidden {
		t.Fatalf("doorstep registered without media:image-bytes.read: status %d, want 403", w.Code)
	}
}

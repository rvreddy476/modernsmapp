package http

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Lane D6 — the internal dating photo routes over httptest with a fake asset
// store, object store and signer. No database, no AWS.

const datingKey = "dating-internal-key"
const datingGPSSecret = "GPS-SECRET-17.3850N-78.4867E"

type fakeDatingStore struct {
	mu       sync.Mutex
	assets   map[uuid.UUID]*postgres.MediaAsset
	variants map[uuid.UUID][]postgres.MediaVariant
	purged   []uuid.UUID
}

func (f *fakeDatingStore) GetMedia(_ context.Context, id uuid.UUID) (*postgres.MediaAsset, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.assets[id]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	cp := *a
	return &cp, nil
}

func (f *fakeDatingStore) GetVariants(_ context.Context, id uuid.UUID) ([]postgres.MediaVariant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]postgres.MediaVariant(nil), f.variants[id]...), nil
}

func (f *fakeDatingStore) InsertVariants(_ context.Context, vs []postgres.MediaVariant) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, v := range vs {
		list := f.variants[v.MediaAssetID]
		replaced := false
		for i := range list {
			if list[i].Name == v.Name {
				list[i], replaced = v, true
			}
		}
		if !replaced {
			list = append(list, v)
		}
		f.variants[v.MediaAssetID] = list
	}
	return nil
}

func (f *fakeDatingStore) MarkDatingPhotoPrepared(_ context.Context, id uuid.UUID, mime string, size int64, w, h int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.assets[id]
	if !ok {
		return pgx.ErrNoRows
	}
	now := time.Now()
	a.AccessScope, a.MetadataStrippedAt, a.MimeType, a.FileSizeBytes, a.Width, a.Height =
		postgres.AccessScopeDatingPhoto, &now, mime, size, &w, &h
	return nil
}

func (f *fakeDatingStore) DeleteAssetForReferrer(_ context.Context, id uuid.UUID, kind string, _ uuid.UUID) (*postgres.AssetPurgeRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.assets[id]
	if !ok {
		return nil, postgres.ErrMediaNotFound
	}
	if kind != service.ReferrerDatingPhoto {
		return nil, postgres.ErrMediaStillReferenced
	}
	keys := []string{a.StorageKey}
	for _, v := range f.variants[id] {
		keys = append(keys, v.ObjectKey)
	}
	delete(f.assets, id)
	delete(f.variants, id)
	f.purged = append(f.purged, id)
	return &postgres.AssetPurgeRecord{MediaID: id, UploaderID: a.UploaderID, Prefix: postgres.AssetPrefix(a.UploaderID, id), ObjectKeys: keys}, nil
}

func (f *fakeDatingStore) ClearBlobReclaim(context.Context, string) error                 { return nil }
func (f *fakeDatingStore) RecordBlobReclaimFailure(context.Context, string, string) error { return nil }

type fakeDatingBlobs struct {
	mu      sync.Mutex
	objects map[string][]byte
	uploads int
}

func (b *fakeDatingBlobs) DownloadObject(_ context.Context, key string) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	d, ok := b.objects[key]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	return append([]byte(nil), d...), nil
}

func (b *fakeDatingBlobs) UploadObject(_ context.Context, key string, data []byte, _ string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.objects[key] = append([]byte(nil), data...)
	b.uploads++
	return nil
}

func (b *fakeDatingBlobs) DeleteObject(_ context.Context, key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.objects, key)
	return nil
}

func (b *fakeDatingBlobs) ListObjectKeys(_ context.Context, prefix string) ([]string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for k := range b.objects {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	return out, nil
}

type fakeDatingSigner struct {
	mu   sync.Mutex
	keys []string
}

func (s *fakeDatingSigner) SignProtected(key string, ttl time.Duration, now time.Time) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys = append(s.keys, key)
	return "https://cdn.test/" + key + "?Expires=" + now.Add(ttl).Format("150405"), nil
}

type datingFixture struct {
	r                        *gin.Engine
	store                    *fakeDatingStore
	blobs                    *fakeDatingBlobs
	signer                   *fakeDatingSigner
	owner, other             uuid.UUID
	photo, foreign, notReady uuid.UUID
	video, unscanned         uuid.UUID
}

// datingJPEG is a 900x600 JPEG carrying EXIF (orientation 6, a GPS IFD and
// an ImageDescription holding datingGPSSecret) and a face test marker.
func datingJPEG(t *testing.T) []byte {
	t.Helper()
	return datingJPEGWithComment(t, processing.MockFaceMarker+"faces=1:subject=owner\n")
}

// datingJPEGWithComment is datingJPEG with its COM segment holding comment
// (no COM segment when empty).
func datingJPEGWithComment(t *testing.T, comment string) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 900, 600))
	for y := 0; y < 600; y++ {
		for x := 0; x < 900; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: uint8((x + y) / 4), A: 255})
		}
	}
	var enc bytes.Buffer
	if err := jpeg.Encode(&enc, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	le := binary.LittleEndian
	desc := append([]byte(datingGPSSecret), 0)
	tiff := []byte{'I', 'I'}
	tiff = le.AppendUint16(tiff, 42)
	tiff = le.AppendUint32(tiff, 8)
	descOff := uint32(8 + 2 + 3*12 + 4)
	gpsOff := descOff + uint32(len(desc))
	tiff = le.AppendUint16(tiff, 3)
	for _, e := range [][4]uint32{{0x010E, 2, uint32(len(desc)), descOff}, {0x0112, 3, 1, 6}, {0x8825, 4, 1, gpsOff}} {
		tiff = le.AppendUint16(tiff, uint16(e[0]))
		tiff = le.AppendUint16(tiff, uint16(e[1]))
		tiff = le.AppendUint32(tiff, e[2])
		tiff = le.AppendUint32(tiff, e[3])
	}
	tiff = le.AppendUint32(tiff, 0)
	tiff = append(tiff, desc...)
	tiff = le.AppendUint16(tiff, 1)
	tiff = le.AppendUint16(tiff, 0x0001)
	tiff = le.AppendUint16(tiff, 2)
	tiff = le.AppendUint32(tiff, 2)
	tiff = append(tiff, 'N', 0, 0, 0)
	tiff = le.AppendUint32(tiff, 0)
	payload := append([]byte("Exif\x00\x00"), tiff...)
	app1 := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(app1[2:], uint16(len(payload)+2))
	app1 = append(app1, payload...)
	var com []byte
	if comment != "" {
		com = []byte{0xFF, 0xFE, 0, 0}
		binary.BigEndian.PutUint16(com[2:], uint16(len(comment)+2))
		com = append(com, comment...)
	}
	raw := enc.Bytes()
	out := append([]byte{}, raw[:2]...)
	out = append(out, app1...)
	out = append(out, com...)
	return append(out, raw[2:]...)
}

func newDatingFixture(t *testing.T) *datingFixture {
	t.Helper()
	f := &datingFixture{
		store:  &fakeDatingStore{assets: map[uuid.UUID]*postgres.MediaAsset{}, variants: map[uuid.UUID][]postgres.MediaVariant{}},
		blobs:  &fakeDatingBlobs{objects: map[string][]byte{}},
		signer: &fakeDatingSigner{},
		owner:  uuid.New(), other: uuid.New(),
		photo: uuid.New(), foreign: uuid.New(), notReady: uuid.New(), video: uuid.New(), unscanned: uuid.New(),
	}
	src := datingJPEG(t)
	add := func(id, uploader uuid.UUID, fileType, status, scanner string) {
		key := "user/" + uploader.String() + "/" + id.String() + "/original"
		a := &postgres.MediaAsset{ID: id, UploaderID: uploader, FileType: fileType, MimeType: "image/jpeg",
			ProcessingStatus: status, ModerationStatus: "passed", StorageKey: key, ModerationScanner: scanner}
		if scanner != "" {
			a.ModerationLabels = []byte(`[{"name":"Swimwear or Underwear","confidence":91.2}]`)
		}
		f.store.assets[id] = a
		f.blobs.objects[key] = src
	}
	add(f.photo, f.owner, "image", "ready", "rekognition")
	add(f.foreign, f.other, "image", "ready", "rekognition")
	add(f.notReady, f.owner, "image", "processing", "rekognition")
	add(f.video, f.owner, "video", "ready", "rekognition")
	add(f.unscanned, f.owner, "image", "ready", "")

	gin.SetMode(gin.TestMode)
	f.r = gin.New()
	svc := service.NewDatingPhotoService(f.store, f.blobs, f.signer, processing.NewMockFaceComparer(), time.Minute, nil)
	New(nil).WithInternalKey(datingKey).WithDatingPhotos(svc).RegisterDatingPhotoRoutes(f.r)
	return f
}

func (f *datingFixture) call(t *testing.T, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", datingKey)
	for k, v := range headers {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	w := httptest.NewRecorder()
	f.r.ServeHTTP(w, req)
	return w
}

func statusPath(media, requester uuid.UUID) string {
	return "/internal/v1/media/dating-photos/" + media.String() + "/owner-status?requester_user_id=" + requester.String()
}

func (f *datingFixture) prepare(t *testing.T, media, requester uuid.UUID) *httptest.ResponseRecorder {
	return f.call(t, http.MethodPost, "/internal/v1/media/dating-photos/"+media.String()+"/prepare",
		map[string]any{"requester_user_id": requester.String(), "detect_faces": true}, nil)
}

func (f *datingFixture) deliver(t *testing.T, media, owner uuid.UUID, variant string) *httptest.ResponseRecorder {
	return f.call(t, http.MethodPost, "/internal/v1/media/dating-photos/"+media.String()+"/delivery-url",
		map[string]any{"owner_user_id": owner.String(), "variant": variant}, nil)
}

func datingStatus(t *testing.T, w *httptest.ResponseRecorder) service.DatingPhotoStatus {
	t.Helper()
	var env struct {
		Data service.DatingPhotoStatus `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	return env.Data
}

func TestDatingPhotoRoutes_ServiceCallersOnly(t *testing.T) {
	f := newDatingFixture(t)
	if w := f.call(t, http.MethodGet, statusPath(f.photo, f.owner), nil, map[string]string{"X-Internal-Service-Key": ""}); w.Code != http.StatusUnauthorized {
		t.Fatalf("no key: %d", w.Code)
	}
	for _, h := range gatewayIdentityHeaders {
		w := f.call(t, http.MethodGet, statusPath(f.photo, f.owner), nil, map[string]string{h: f.owner.String()})
		if w.Code != http.StatusForbidden || errCode(t, w) != CodeUserCallerRefused {
			t.Fatalf("key + %s: %d %s", h, w.Code, w.Body.String())
		}
	}
}

func TestDatingPhotoOwnerStatus_RefusesForeignMedia(t *testing.T) {
	f := newDatingFixture(t)
	for name, id := range map[string]uuid.UUID{"foreign": f.foreign, "missing": uuid.New()} {
		w := f.call(t, http.MethodGet, statusPath(id, f.owner), nil, nil)
		if w.Code != http.StatusNotFound || errCode(t, w) != CodeMediaNotFound {
			t.Fatalf("%s: %d %s; want 404 MEDIA_NOT_FOUND", name, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "moderation") {
			t.Fatalf("%s: refusal leaks moderation data: %s", name, w.Body.String())
		}
	}
	w := f.call(t, http.MethodGet, statusPath(f.photo, f.owner), nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("own: %d %s", w.Code, w.Body.String())
	}
	st := datingStatus(t, w)
	if !st.OwnerMatches || st.Status != "ready" || st.ModerationStatus != "passed" || st.Kind != "image" ||
		!st.ModerationScanned || st.ModerationScanner != "rekognition" || len(st.ModerationLabels) != 1 ||
		st.ModerationLabels[0].Name != "Swimwear or Underwear" || st.Prepared {
		t.Fatalf("own status = %+v", st)
	}
	if st := datingStatus(t, f.call(t, http.MethodGet, statusPath(f.unscanned, f.owner), nil, nil)); st.ModerationScanned || st.ModerationLabels == nil {
		t.Fatalf("unscanned status = %+v; want moderation_scanned=false with an empty list", st)
	}
	if w := f.call(t, http.MethodGet, "/internal/v1/media/dating-photos/"+f.photo.String()+"/owner-status?requester_user_id=nope", nil, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("bad requester: %d", w.Code)
	}
}

func TestDatingPhotoPrepare_BlurredVariantAndServedBytesStripped(t *testing.T) {
	f := newDatingFixture(t)
	if w := f.deliver(t, f.photo, f.owner, "blurred"); w.Code != http.StatusConflict || errCode(t, w) != CodeMediaNotPrepared {
		t.Fatalf("delivery before prepare: %d %s; want 409 MEDIA_NOT_PREPARED", w.Code, w.Body.String())
	}

	w := f.prepare(t, f.photo, f.owner)
	if w.Code != http.StatusOK {
		t.Fatalf("prepare: %d %s", w.Code, w.Body.String())
	}
	st := datingStatus(t, w)
	if !st.Prepared || st.ContentType != "image/jpeg" || st.FaceCount == nil || *st.FaceCount != 1 ||
		st.Width == nil || st.Height == nil || *st.Width != 600 || *st.Height != 900 {
		t.Fatalf("prepared status = %+v (want prepared, 600x900 after orientation, face_count 1)", st)
	}

	var blurKey string
	for _, v := range f.store.variants[f.photo] {
		if v.Name == processing.DatingBlurVariant {
			blurKey = v.ObjectKey
		}
	}
	if blurKey == "" || strings.Contains(blurKey, f.photo.String()) || strings.Contains(blurKey, "original") {
		t.Fatalf("blurred variant key = %q; want one that names neither the media id nor the original", blurKey)
	}

	for _, variant := range []string{"blurred", "full"} {
		w := f.deliver(t, f.photo, f.owner, variant)
		if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("deliver %s: %d %s", variant, w.Code, w.Body.String())
		}
	}
	if len(f.signer.keys) != 2 || f.signer.keys[0] != blurKey {
		t.Fatalf("signed keys = %v; want the blurred key first", f.signer.keys)
	}
	fullKey := f.signer.keys[1]
	if fullKey == blurKey {
		t.Fatal("full and blurred deliveries signed the same object")
	}

	// Every object a dating photo URL can point at is free of EXIF/GPS and
	// of the uploaded comment. The fixture wires the local mock face
	// provider, which appends its rebuilt face marker after the JPEG's EOI
	// (never inside the image, never on the blurred variant).
	assertServedDatingBytes(t, f, f.photo, "\n"+processing.MockFaceMarker+"faces=1:subject=owner\n")
	blurred, err := jpeg.Decode(bytes.NewReader(f.blobs.objects[blurKey]))
	if err != nil || blurred.Bounds().Dx() > 240 || blurred.Bounds().Dy() > 240 {
		t.Fatalf("blurred decode = %v, %v; want a small JPEG", err, blurred)
	}

	// Idempotent: a second prepare re-encodes nothing.
	uploads := f.blobs.uploads
	if w := f.prepare(t, f.photo, f.owner); w.Code != http.StatusOK || f.blobs.uploads != uploads {
		t.Fatalf("second prepare: %d, uploads %d -> %d", w.Code, uploads, f.blobs.uploads)
	}
}

func TestDatingPhotoPrepare_RefusesForeignAndNotReady(t *testing.T) {
	f := newDatingFixture(t)
	if w := f.prepare(t, f.foreign, f.owner); w.Code != http.StatusNotFound {
		t.Fatalf("foreign: %d %s", w.Code, w.Body.String())
	}
	for name, id := range map[string]uuid.UUID{"processing": f.notReady, "video": f.video} {
		if w := f.prepare(t, id, f.owner); w.Code != http.StatusConflict || errCode(t, w) != CodeMediaNotReady {
			t.Fatalf("%s: %d %s; want 409 MEDIA_NOT_READY", name, w.Code, w.Body.String())
		}
	}
	if f.blobs.uploads != 0 {
		t.Fatalf("refused prepares wrote %d objects", f.blobs.uploads)
	}
}

func TestDatingPhotoDeliveryURL_OwnerAndVariantChecked(t *testing.T) {
	f := newDatingFixture(t)
	if w := f.prepare(t, f.photo, f.owner); w.Code != http.StatusOK {
		t.Fatalf("prepare: %d", w.Code)
	}
	if w := f.deliver(t, f.photo, f.other, "blurred"); w.Code != http.StatusNotFound {
		t.Fatalf("other owner: %d %s", w.Code, w.Body.String())
	}
	if w := f.deliver(t, f.photo, f.owner, "original"); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown variant: %d %s", w.Code, w.Body.String())
	}
	if len(f.signer.keys) != 0 {
		t.Fatalf("refused deliveries signed %v", f.signer.keys)
	}
}

func TestDatingPhotoDelete_OwnerCheckedAndRemovesVariants(t *testing.T) {
	f := newDatingFixture(t)
	if w := f.prepare(t, f.photo, f.owner); w.Code != http.StatusOK {
		t.Fatalf("prepare: %d", w.Code)
	}
	path := "/internal/v1/media/dating-photos/" + f.photo.String() + "?requester_user_id="
	if w := f.call(t, http.MethodDelete, path+f.other.String(), nil, nil); w.Code != http.StatusNotFound || len(f.store.purged) != 0 {
		t.Fatalf("delete by another user: %d, purged %v", w.Code, f.store.purged)
	}
	before := len(f.blobs.objects)
	w := f.call(t, http.MethodDelete, path+f.owner.String(), nil, nil)
	if w.Code != http.StatusOK || len(f.store.purged) != 1 {
		t.Fatalf("owner delete: %d %s", w.Code, w.Body.String())
	}
	for key := range f.blobs.objects {
		if strings.Contains(key, f.photo.String()) || strings.Contains(key, "dating-blurred") {
			t.Fatalf("object %s survived the delete", key)
		}
	}
	if len(f.blobs.objects) >= before {
		t.Fatalf("objects %d -> %d", before, len(f.blobs.objects))
	}
	if w := f.call(t, http.MethodDelete, path+f.owner.String(), nil, nil); w.Code != http.StatusNotFound {
		t.Fatalf("second delete: %d; want 404 (already gone)", w.Code)
	}
}

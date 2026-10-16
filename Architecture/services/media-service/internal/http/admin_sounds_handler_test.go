package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/media-service/internal/store/blob"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// The admin sound library routes over httptest (2026-10-16): the REAL
// handler, the REAL token check and the REAL service.AdminSounds, on an
// in-memory store and object store, with ffprobe replaced (the test
// machines have no ffmpeg). The store's SQL (drafts never public, withdraw
// silencing the sound, the CHECKs) is pinned against PostgreSQL in
// store/postgres/admin_sounds_integration_test.go.

// ── fakes ───────────────────────────────────────────────────────────────

type fakeAdminSoundStore struct {
	mu     sync.Mutex
	rows   map[uuid.UUID]*postgres.AdminLibrarySound
	tracks map[uuid.UUID]string // library id → materialised sound status
	fail   error
}

func newFakeAdminSoundStore() *fakeAdminSoundStore {
	return &fakeAdminSoundStore{rows: map[uuid.UUID]*postgres.AdminLibrarySound{}, tracks: map[uuid.UUID]string{}}
}

func (f *fakeAdminSoundStore) snapshot(r *postgres.AdminLibrarySound) *postgres.AdminLibrarySound {
	cp := *r
	cp.Status = cp.DeriveStatus()
	return &cp
}

func (f *fakeAdminSoundStore) ListAdminLibrarySounds(_ context.Context, flt postgres.AdminSoundFilter) ([]postgres.AdminLibrarySound, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var all []postgres.AdminLibrarySound
	for _, r := range f.rows {
		s := f.snapshot(r)
		if flt.Status != "" && flt.Status != "all" && s.Status != flt.Status &&
			!(flt.Status == postgres.LibraryStatusInactive && s.Status == postgres.LibraryStatusDraft) {
			continue
		}
		if flt.Q != "" && !strings.Contains(strings.ToLower(s.Title+" "+s.Artist), strings.ToLower(flt.Q)) {
			continue
		}
		if flt.AfterAt != nil && !(s.CreatedAt.Before(*flt.AfterAt) || (s.CreatedAt.Equal(*flt.AfterAt) && s.ID.String() < flt.AfterID.String())) {
			continue
		}
		all = append(all, *s)
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].CreatedAt.Equal(all[j].CreatedAt) {
			return all[i].CreatedAt.After(all[j].CreatedAt)
		}
		return all[i].ID.String() > all[j].ID.String()
	})
	if len(all) > flt.Limit {
		all = all[:flt.Limit]
	}
	return all, nil
}

func (f *fakeAdminSoundStore) GetAdminLibrarySound(_ context.Context, id uuid.UUID) (*postgres.AdminLibrarySound, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[id]
	if !ok {
		return nil, nil
	}
	return f.snapshot(r), nil
}

func (f *fakeAdminSoundStore) AdminLibraryKeyInUse(_ context.Context, key string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if r.AudioURL == key {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeAdminSoundStore) CreateAdminLibrarySound(_ context.Context, in postgres.AdminLibrarySoundInsert) (*postgres.AdminLibrarySound, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	by, lt, rh, genre := in.CreatedBy, in.LicenseType, in.RightsHolder, in.Genre
	r := &postgres.AdminLibrarySound{
		ID: uuid.New(), Title: in.Title, Artist: in.Artist, Genre: &genre, Mood: in.Mood, DurationMs: in.DurationMs,
		LicenseType: &lt, RightsHolder: &rh, LicenseReference: in.LicenseReference, LicenseExpiresAt: in.LicenseExpiresAt,
		Attribution: in.Attribution, IsActive: in.IsActive, CreatedAt: now, UpdatedAt: now, CreatedBy: &by, AudioURL: in.StorageKey,
	}
	if in.IsActive {
		r.FirstActivatedAt = &now
	}
	f.rows[r.ID] = r
	return f.snapshot(r), nil
}

func (f *fakeAdminSoundStore) PatchAdminLibrarySound(_ context.Context, id uuid.UUID, p postgres.AdminLibrarySoundPatch, now time.Time) (*postgres.AdminLibrarySound, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[id]
	if !ok {
		return nil, nil
	}
	exp := r.LicenseExpiresAt
	if p.LicenseExpiresAt.Set {
		exp = p.LicenseExpiresAt.Value
	}
	if p.IsActive != nil && *p.IsActive {
		if r.WithdrawnAt != nil {
			return nil, postgres.ErrLibrarySoundWithdrawn
		}
		if exp != nil && !exp.After(now) {
			return nil, postgres.ErrLibraryLicenseExpired
		}
	}
	if p.Title.Set {
		r.Title = *p.Title.Value
	}
	if p.Artist.Set {
		r.Artist = *p.Artist.Value
	}
	if p.Genre.Set {
		r.Genre = p.Genre.Value
	}
	if p.Mood.Set {
		r.Mood = p.Mood.Value
	}
	if p.RightsHolder.Set {
		r.RightsHolder = p.RightsHolder.Value
	}
	if p.LicenseReference.Set {
		r.LicenseReference = p.LicenseReference.Value
	}
	if p.Attribution.Set {
		r.Attribution = p.Attribution.Value
	}
	r.LicenseExpiresAt = exp
	if p.IsActive != nil {
		r.IsActive = *p.IsActive
		if r.IsActive && r.FirstActivatedAt == nil {
			r.FirstActivatedAt = &now
		}
	}
	r.UpdatedAt = now
	return f.snapshot(r), nil
}

func (f *fakeAdminSoundStore) WithdrawAdminLibrarySound(_ context.Context, id uuid.UUID, scope, reason string) (*postgres.AdminLibrarySound, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[id]
	if !ok {
		return nil, nil
	}
	now := time.Now()
	r.IsActive = false
	if r.WithdrawnAt == nil {
		r.WithdrawnAt = &now
	}
	eff := scope
	if r.WithdrawScope != nil && *r.WithdrawScope == postgres.WithdrawEverywhere {
		eff = postgres.WithdrawEverywhere
	}
	r.WithdrawScope, r.WithdrawReason = &eff, &reason
	if eff == postgres.WithdrawEverywhere {
		if _, used := f.tracks[id]; used {
			f.tracks[id] = "deleted"
		}
	}
	return f.snapshot(r), nil
}

type fakeAdminBlobs struct {
	mu      sync.Mutex
	objects map[string][]byte
	stat    map[string]int64 // overrides the reported size
	putTTL  map[string]time.Duration
	getTTL  map[string]time.Duration
}

func newFakeAdminBlobs() *fakeAdminBlobs {
	return &fakeAdminBlobs{objects: map[string][]byte{}, stat: map[string]int64{},
		putTTL: map[string]time.Duration{}, getTTL: map[string]time.Duration{}}
}

func (b *fakeAdminBlobs) GeneratePresignedPutURL(_ context.Context, key string, ttl time.Duration) (*url.URL, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.putTTL[key] = ttl
	return url.Parse("https://minio.test/media/" + key + "?X-Amz-Signature=put")
}

func (b *fakeAdminBlobs) GeneratePresignedGetURL(_ context.Context, key string, ttl time.Duration) (*url.URL, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.getTTL[key] = ttl
	return url.Parse("https://minio.test/media/" + key + "?X-Amz-Signature=get")
}

func (b *fakeAdminBlobs) StatObject(_ context.Context, key string) (blob.ObjectInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	data, ok := b.objects[key]
	if !ok {
		return blob.ObjectInfo{}, fmt.Errorf("%w: %s", blob.ErrObjectNotFound, key)
	}
	size := int64(len(data))
	if s, ok := b.stat[key]; ok {
		size = s
	}
	return blob.ObjectInfo{Size: size, ContentType: "audio/mp4"}, nil
}

func (b *fakeAdminBlobs) ReadObjectRange(_ context.Context, key string, start, end int64) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	data, ok := b.objects[key]
	if !ok {
		return nil, blob.ErrObjectNotFound
	}
	if end+1 > int64(len(data)) {
		end = int64(len(data)) - 1
	}
	return data[start : end+1], nil
}

func (b *fakeAdminBlobs) DownloadObject(_ context.Context, key string) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	data, ok := b.objects[key]
	if !ok {
		return nil, blob.ErrObjectNotFound
	}
	return data, nil
}

// m4aBytes starts like an M4A (ftyp box).
var m4aBytes = append([]byte{0, 0, 0, 0x20, 'f', 't', 'y', 'p', 'M', '4', 'A', ' '}, make([]byte, 64)...)

// ── rig ─────────────────────────────────────────────────────────────────

type adminSoundRig struct {
	r      *gin.Engine
	store  *fakeAdminSoundStore
	blobs  *fakeAdminBlobs
	signer *servicetoken.Signer
	probe  *processing.AudioFileProbe
	probed []string
	now    time.Time
}

func newAdminSoundRig(t *testing.T) *adminSoundRig {
	t.Helper()
	gin.SetMode(gin.TestMode)
	signer, pub := mustSigner(t, "admin-service", "a1")
	rg := &adminSoundRig{
		r: gin.New(), store: newFakeAdminSoundStore(), blobs: newFakeAdminBlobs(), signer: signer,
		probe: &processing.AudioFileProbe{HasAudio: true, Codec: "aac", DurationMs: 61_500},
		now:   time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC),
	}
	svc := service.NewAdminSounds(rg.store, rg.blobs).
		WithProbe(func(_ context.Context, path string) (*processing.AudioFileProbe, error) {
			rg.probed = append(rg.probed, path)
			if rg.probe == nil {
				return nil, fmt.Errorf("ffprobe: invalid data")
			}
			cp := *rg.probe
			return &cp, nil
		}).
		WithClock(func() time.Time { return rg.now })
	h := New(nil).WithAdminSounds(svc)
	if err := h.RegisterAdminSoundLibrary(rg.r, "a1", pub); err != nil {
		t.Fatal(err)
	}
	return rg
}

func (rg *adminSoundRig) token(t *testing.T, scope, actor string) string {
	t.Helper()
	tok, err := rg.signer.Mint("media", "admin-console", []string{scope}, nil, time.Minute, servicetoken.WithActor(actor))
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func (rg *adminSoundRig) call(t *testing.T, method, path, body, scope string) *httptest.ResponseRecorder {
	t.Helper()
	return rg.callAs(t, method, path, body, scope, uuid.NewString())
}

func (rg *adminSoundRig) callAs(t *testing.T, method, path, body, scope, actor string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, AdminSoundsPrefix+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(ServiceAuthHeader, "Bearer "+rg.token(t, scope, actor))
	w := httptest.NewRecorder()
	rg.r.ServeHTTP(w, req)
	return w
}

func soundErrCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct{ Code string } `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	return env.Error.Code
}

func decodeSound(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil || env.Data == nil {
		t.Fatalf("no data in %d %s", w.Code, w.Body.String())
	}
	return env.Data
}

// upload puts an object at a fresh library key and returns the key.
func (rg *adminSoundRig) upload(data []byte) string {
	key := service.LibraryUploadPrefix + uuid.NewString() + ".m4a"
	rg.blobs.objects[key] = data
	return key
}

func createBody(key string, extra string) string {
	return `{"storage_key":"` + key + `","title":"Monsoon","artist":"Momentum Library","genre":"Pop","mood":"Calm",` +
		`"license_type":"royalty_free","rights_holder":"Momentum Records"` + extra + `}`
}

func (rg *adminSoundRig) create(t *testing.T, extra string) map[string]any {
	t.Helper()
	w := rg.call(t, http.MethodPost, "", createBody(rg.upload(m4aBytes), extra), OpSoundsManage)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	return decodeSound(t, w)
}

// ── caller check ────────────────────────────────────────────────────────

func TestAdminSoundRoutes_OnlyAnAdminServiceTokenForTheRouteScope(t *testing.T) {
	rg := newAdminSoundRig(t)
	id := uuid.NewString()
	routes := []struct{ method, path, body, scope string }{
		{http.MethodGet, "", "", OpSoundsRead},
		{http.MethodGet, "/" + id, "", OpSoundsRead},
		{http.MethodGet, "/" + id + "/preview", "", OpSoundsRead},
		{http.MethodPost, "/uploads", `{}`, OpSoundsManage},
		{http.MethodPost, "", `{}`, OpSoundsManage},
		{http.MethodPatch, "/" + id, `{}`, OpSoundsManage},
		{http.MethodPost, "/" + id + "/withdraw", `{}`, OpSoundsManage},
	}
	otherIssuer, _ := mustSigner(t, "commerce-service", "a1")
	for _, rt := range routes {
		send := func(token string, hdr map[string]string) int {
			req := httptest.NewRequest(rt.method, AdminSoundsPrefix+rt.path, strings.NewReader(rt.body))
			if token != "" {
				req.Header.Set(ServiceAuthHeader, "Bearer "+token)
			}
			for k, v := range hdr {
				req.Header.Set(k, v)
			}
			w := httptest.NewRecorder()
			rg.r.ServeHTTP(w, req)
			if w.Code == http.StatusForbidden || w.Code == http.StatusUnauthorized {
				if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") && token != "" && hdr == nil {
					t.Fatalf("%s %s: refusal is cacheable", rt.method, rt.path)
				}
			}
			return w.Code
		}
		other := OpSoundsRead
		if rt.scope == OpSoundsRead {
			other = OpSoundsManage
		}
		mint := func(s *servicetoken.Signer, aud, scope, actor string) string {
			tok, err := s.Mint(aud, "admin-console", []string{scope}, nil, time.Minute, servicetoken.WithActor(actor))
			if err != nil {
				t.Fatal(err)
			}
			return tok
		}
		actor := uuid.NewString()
		for name, c := range map[string]struct {
			token string
			hdr   map[string]string
			want  int
		}{
			"no token":       {"", nil, http.StatusUnauthorized},
			"internal key":   {"", map[string]string{"X-Internal-Service-Key": "legacy"}, http.StatusUnauthorized},
			"other scope":    {mint(rg.signer, "media", other, actor), nil, http.StatusForbidden},
			"wrong audience": {mint(rg.signer, "post", rt.scope, actor), nil, http.StatusForbidden},
			"other issuer":   {mint(otherIssuer, "media", rt.scope, actor), nil, http.StatusForbidden},
			"no actor":       {mint(rg.signer, "media", rt.scope, ""), nil, http.StatusForbidden},
			"gateway user":   {mint(rg.signer, "media", rt.scope, actor), map[string]string{"X-User-Id": actor}, http.StatusForbidden},
		} {
			if got := send(c.token, c.hdr); got != c.want {
				t.Fatalf("%s %s %s: %d, want %d", rt.method, rt.path, name, got, c.want)
			}
		}
		// The right token gets past the check (to the route's own answer).
		if got := send(mint(rg.signer, "media", rt.scope, actor), nil); got == http.StatusUnauthorized || got == http.StatusForbidden {
			t.Fatalf("%s %s with %s: %d", rt.method, rt.path, rt.scope, got)
		}
	}
	if len(rg.store.rows) != 0 || len(rg.blobs.putTTL) != 0 {
		t.Fatal("a refused call wrote something")
	}
}

func TestAdminSoundRoutes_NoKeyRefusesEverything(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	signer, _ := mustSigner(t, "admin-service", "a1")
	if err := New(nil).WithAdminSounds(service.NewAdminSounds(newFakeAdminSoundStore(), newFakeAdminBlobs())).
		RegisterAdminSoundLibrary(r, "a1", ""); err != nil {
		t.Fatal(err)
	}
	tok, _ := signer.Mint("media", "admin-console", []string{OpSoundsRead}, nil, time.Minute, servicetoken.WithActor(uuid.NewString()))
	req := httptest.NewRequest(http.MethodGet, AdminSoundsPrefix, nil)
	req.Header.Set(ServiceAuthHeader, "Bearer "+tok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("no verifier key: %d", w.Code)
	}
}

// The routes sit beside every other media route without a conflict.
func TestAdminSoundRoutes_RegisterBesideTheMediaRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	noop := func(c *gin.Context) { c.Next() }
	h := New(nil).WithInternalKey("k")
	h.RegisterRoutes(r, noop, noop)
	h.RegisterAudioRoutes(r, noop)
	if err := h.RegisterPartnerEvidence(r, "a1", ""); err != nil {
		t.Fatal(err)
	}
	if err := h.RegisterAdminSoundLibrary(r, "a1", ""); err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, ri := range r.Routes() {
		if strings.HasPrefix(ri.Path, AdminSoundsPrefix) {
			found++
		}
	}
	if found != 7 {
		t.Fatalf("%d admin sound routes, want 7", found)
	}
}

// ── uploads ─────────────────────────────────────────────────────────────

func TestAdminSoundUploads_PresignRules(t *testing.T) {
	rg := newAdminSoundRig(t)
	for ct, ext := range map[string]string{"audio/mpeg": ".mp3", "audio/mp4": ".m4a", "audio/aac": ".aac",
		"audio/x-m4a": ".m4a", "audio/wav": ".wav", "audio/ogg": ".ogg"} {
		w := rg.call(t, http.MethodPost, "/uploads", `{"filename":"song`+ext+`","content_type":"`+ct+`","size_bytes":20971520}`, OpSoundsManage)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", ct, w.Code, w.Body.String())
		}
		d := decodeSound(t, w)
		key, _ := d["storage_key"].(string)
		hdrs, _ := d["headers"].(map[string]any)
		if !strings.HasPrefix(key, "audio-library/") || !strings.HasSuffix(key, ext) || d["method"] != "PUT" ||
			hdrs["Content-Type"] != ct || !strings.Contains(d["upload_url"].(string), key) {
			t.Fatalf("%s: %+v", ct, d)
		}
		if rg.blobs.putTTL[key] != 15*time.Minute {
			t.Fatalf("%s presigned for %v", ct, rg.blobs.putTTL[key])
		}
		exp, err := time.Parse(time.RFC3339, d["expires_at"].(string))
		if err != nil || !exp.Equal(rg.now.Add(15*time.Minute)) {
			t.Fatalf("expires_at %v (%v)", d["expires_at"], err)
		}
	}
	for name, body := range map[string]string{
		"video":       `{"filename":"a.mp4","content_type":"video/mp4","size_bytes":10}`,
		"flac":        `{"filename":"a.flac","content_type":"audio/flac","size_bytes":10}`,
		"too big":     `{"filename":"a.mp3","content_type":"audio/mpeg","size_bytes":20971521}`,
		"empty":       `{"filename":"a.mp3","content_type":"audio/mpeg","size_bytes":0}`,
		"no filename": `{"filename":"  ","content_type":"audio/mpeg","size_bytes":10}`,
		"not json":    `size=10`,
		"wrong type":  `{"filename":"a.mp3","content_type":"audio/mpeg","size_bytes":"10"}`,
	} {
		w := rg.call(t, http.MethodPost, "/uploads", body, OpSoundsManage)
		if w.Code != http.StatusBadRequest || soundErrCode(t, w) != "VALIDATION_ERROR" {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
}

// ── create ──────────────────────────────────────────────────────────────

func TestAdminSoundCreate_ChecksTheUpload(t *testing.T) {
	rg := newAdminSoundRig(t)
	actor := uuid.NewString()
	key := rg.upload(m4aBytes)
	w := rg.callAs(t, http.MethodPost, "", createBody(key, ""), OpSoundsManage, actor)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	d := decodeSound(t, w)
	if d["status"] != "draft" || d["is_active"] != false || d["duration_ms"] != float64(61_500) ||
		d["created_by"] != actor || d["genre"] != "pop" || d["mood"] != "calm" || d["license_type"] != "royalty_free" ||
		d["rights_holder"] != "Momentum Records" || d["audio_track_id"] != nil || d["usage_count"] != float64(0) {
		t.Fatalf("created %+v", d)
	}
	for _, k := range []string{"id", "title", "artist", "license_reference", "license_expires_at", "attribution", "created_at", "updated_at"} {
		if _, ok := d[k]; !ok {
			t.Fatalf("LibrarySound lacks %q: %+v", k, d)
		}
	}
	if strings.Contains(w.Body.String(), "audio-library/") || strings.Contains(w.Body.String(), "minio") {
		t.Fatalf("a storage key or object URL left the service: %s", w.Body.String())
	}
	if len(rg.probed) != 1 || !strings.HasSuffix(rg.probed[0], ".m4a") {
		t.Fatalf("probed %v", rg.probed)
	}
	// The same upload cannot back a second sound.
	if w := rg.call(t, http.MethodPost, "", createBody(key, ""), OpSoundsManage); w.Code != http.StatusConflict || soundErrCode(t, w) != "STORAGE_KEY_IN_USE" {
		t.Fatalf("reuse: %d %s", w.Code, w.Body.String())
	}
	// Enabled on create when asked.
	if d := rg.create(t, `,"is_active":true`); d["status"] != "active" {
		t.Fatalf("active on create: %+v", d)
	}

	cases := []struct {
		name   string
		setup  func() string // returns the key
		probe  *processing.AudioFileProbe
		status int
		code   string
	}{
		{"missing", func() string { return service.LibraryUploadPrefix + uuid.NewString() + ".mp3" }, nil, 422, "UPLOAD_MISSING"},
		{"text", func() string { return rg.upload([]byte("#!/bin/sh\necho this is not audio at all, really")) }, nil, 422, "NOT_AUDIO"},
		{"flac", func() string { return rg.upload(append([]byte("fLaC"), make([]byte, 60)...)) }, nil, 422, "NOT_AUDIO"},
		{"empty", func() string { k := rg.upload(m4aBytes); rg.blobs.stat[k] = 0; return k }, nil, 422, "NOT_AUDIO"},
		{"too large", func() string { k := rg.upload(m4aBytes); rg.blobs.stat[k] = 20<<20 + 1; return k }, nil, 422, "TOO_LARGE"},
		{"undecodable", func() string { return rg.upload(m4aBytes) }, nil, 422, "NOT_AUDIO"},
		{"video", func() string { return rg.upload(m4aBytes) }, &processing.AudioFileProbe{HasAudio: true, HasVideo: true, DurationMs: 1000}, 422, "NOT_AUDIO"},
		{"no audio", func() string { return rg.upload(m4aBytes) }, &processing.AudioFileProbe{DurationMs: 1000}, 422, "NOT_AUDIO"},
		{"no duration", func() string { return rg.upload(m4aBytes) }, &processing.AudioFileProbe{HasAudio: true}, 422, "NOT_AUDIO"},
		{"too long", func() string { return rg.upload(m4aBytes) }, &processing.AudioFileProbe{HasAudio: true, DurationMs: 300_001}, 422, "TOO_LONG"},
	}
	for _, c := range cases {
		rg.probe = c.probe
		before := len(rg.store.rows)
		w := rg.call(t, http.MethodPost, "", createBody(c.setup(), ""), OpSoundsManage)
		if w.Code != c.status || soundErrCode(t, w) != c.code {
			t.Fatalf("%s: %d %s, want %d %s", c.name, w.Code, w.Body.String(), c.status, c.code)
		}
		if len(rg.store.rows) != before {
			t.Fatalf("%s: a refused upload made a row", c.name)
		}
	}
	// Exactly 300 s is allowed.
	rg.probe = &processing.AudioFileProbe{HasAudio: true, DurationMs: 300_000}
	if d := rg.create(t, ""); d["duration_ms"] != float64(300_000) {
		t.Fatalf("300 s: %+v", d)
	}
}

func TestAdminSoundCreate_Validation(t *testing.T) {
	rg := newAdminSoundRig(t)
	good := rg.upload(m4aBytes)
	for name, body := range map[string]string{
		"key outside the prefix": createBody("user/"+uuid.NewString()+"/a.m4a", ""),
		"key that climbs":        createBody("audio-library/../user/x.m4a", ""),
		"key with another ext":   createBody("audio-library/"+uuid.NewString()+".exe", ""),
		"no title":               strings.Replace(createBody(good, ""), `"title":"Monsoon"`, `"title":" "`, 1),
		"no artist":              strings.Replace(createBody(good, ""), `"artist":"Momentum Library"`, `"artist":""`, 1),
		"no genre":               strings.Replace(createBody(good, ""), `"genre":"Pop"`, `"genre":""`, 1),
		"bad licence":            strings.Replace(createBody(good, ""), `"royalty_free"`, `"creative_commons"`, 1),
		"no rights holder":       strings.Replace(createBody(good, ""), `"rights_holder":"Momentum Records"`, `"rights_holder":""`, 1),
		"long title":             strings.Replace(createBody(good, ""), `"title":"Monsoon"`, `"title":"`+strings.Repeat("x", 201)+`"`, 1),
		"long attribution":       createBody(good, `,"attribution":"`+strings.Repeat("x", 501)+`"`),
		"bad expiry":             createBody(good, `,"license_expires_at":"next year"`),
		"not json":               `{"storage_key":`,
	} {
		w := rg.call(t, http.MethodPost, "", body, OpSoundsManage)
		if w.Code != http.StatusBadRequest || soundErrCode(t, w) != "VALIDATION_ERROR" {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	// Enabling a sound whose licence has lapsed is refused; a draft with
	// one is kept.
	past := rg.now.Add(-time.Hour).Format(time.RFC3339)
	if w := rg.call(t, http.MethodPost, "", createBody(good, `,"is_active":true,"license_expires_at":"`+past+`"`), OpSoundsManage); w.Code != 422 || soundErrCode(t, w) != "LICENSE_EXPIRED" {
		t.Fatalf("expired + active: %d %s", w.Code, w.Body.String())
	}
	if len(rg.store.rows) != 0 || len(rg.probed) != 0 {
		t.Fatal("an invalid create reached the store or ffprobe")
	}
}

// ── read, patch, withdraw, preview ──────────────────────────────────────

func TestAdminSoundPatch(t *testing.T) {
	rg := newAdminSoundRig(t)
	id := rg.create(t, "")["id"].(string)
	path := "/" + id

	w := rg.call(t, http.MethodPatch, path, `{"is_active":true,"title":" Monsoon (Edit) ","mood":null,"reason":"cleared by legal"}`, OpSoundsManage)
	if w.Code != http.StatusOK {
		t.Fatalf("enable: %d %s", w.Code, w.Body.String())
	}
	if d := decodeSound(t, w); d["status"] != "active" || d["title"] != "Monsoon (Edit)" || d["mood"] != nil {
		t.Fatalf("enabled %+v", d)
	}
	w = rg.call(t, http.MethodPatch, path, `{"is_active":false}`, OpSoundsManage)
	if d := decodeSound(t, w); d["status"] != "inactive" {
		t.Fatalf("disabled %+v", d)
	}
	// The console sends end of day in IST, with an offset; it is stored as
	// that instant.
	w = rg.call(t, http.MethodPatch, path, `{"license_expires_at":"2026-12-31T23:59:59+05:30","license_reference":"INV-9","attribution":"Music by Momentum"}`, OpSoundsManage)
	if d := decodeSound(t, w); w.Code != 200 || d["license_reference"] != "INV-9" || d["license_expires_at"] != "2026-12-31T18:29:59Z" {
		t.Fatalf("licence edit: %d %+v", w.Code, d)
	}
	// null clears every optional field.
	w = rg.call(t, http.MethodPatch, path, `{"license_expires_at":null,"license_reference":null,"attribution":null,"mood":null}`, OpSoundsManage)
	if d := decodeSound(t, w); w.Code != 200 || d["license_reference"] != nil || d["license_expires_at"] != nil || d["attribution"] != nil || d["mood"] != nil {
		t.Fatalf("clear: %d %+v", w.Code, d)
	}

	for name, c := range map[string]struct {
		body   string
		status int
		code   string
	}{
		"empty":            {`{}`, 400, "VALIDATION_ERROR"},
		"reason only":      {`{"reason":"why"}`, 400, "VALIDATION_ERROR"},
		"storage key":      {`{"storage_key":"audio-library/x.m4a"}`, 400, "VALIDATION_ERROR"},
		"license type":     {`{"license_type":"original"}`, 400, "VALIDATION_ERROR"},
		"clear title":      {`{"title":null}`, 400, "VALIDATION_ERROR"},
		"blank artist":     {`{"artist":"  "}`, 400, "VALIDATION_ERROR"},
		"is_active string": {`{"is_active":"yes"}`, 400, "VALIDATION_ERROR"},
		"array":            {`[1]`, 400, "VALIDATION_ERROR"},
		"expired enable":   {`{"is_active":true,"license_expires_at":"` + rg.now.Add(-time.Minute).Format(time.RFC3339) + `"}`, 422, "LICENSE_EXPIRED"},
	} {
		w := rg.call(t, http.MethodPatch, path, c.body, OpSoundsManage)
		if w.Code != c.status || soundErrCode(t, w) != c.code {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	if w := rg.call(t, http.MethodPatch, "/"+uuid.NewString(), `{"title":"x"}`, OpSoundsManage); w.Code != 404 {
		t.Fatalf("missing: %d", w.Code)
	}
	if w := rg.call(t, http.MethodPatch, "/not-a-uuid", `{"title":"x"}`, OpSoundsManage); w.Code != 400 {
		t.Fatalf("bad id: %d", w.Code)
	}
}

func TestAdminSoundWithdraw(t *testing.T) {
	rg := newAdminSoundRig(t)
	for _, scope := range []string{"new_uses", "everywhere"} {
		id := rg.create(t, `,"is_active":true`)["id"].(string)
		libID := uuid.MustParse(id)
		rg.store.tracks[libID] = "ready" // used once: materialised
		for name, body := range map[string]string{
			"no reason":    `{"scope":"` + scope + `"}`,
			"short reason": `{"scope":"` + scope + `","reason":" ab "}`,
			"long reason":  `{"scope":"` + scope + `","reason":"` + strings.Repeat("x", 501) + `"}`,
			"bad scope":    `{"scope":"some","reason":"licence lapsed"}`,
		} {
			if w := rg.call(t, http.MethodPost, "/"+id+"/withdraw", body, OpSoundsManage); w.Code != 400 || soundErrCode(t, w) != "VALIDATION_ERROR" {
				t.Fatalf("%s %s: %d %s", scope, name, w.Code, w.Body.String())
			}
		}
		w := rg.call(t, http.MethodPost, "/"+id+"/withdraw", `{"scope":"`+scope+`","reason":"licence lapsed"}`, OpSoundsManage)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", scope, w.Code, w.Body.String())
		}
		d := decodeSound(t, w)
		if d["status"] != "withdrawn" || d["is_active"] != false || d["withdraw_scope"] != scope || d["withdraw_reason"] != "licence lapsed" {
			t.Fatalf("%s: %+v", scope, d)
		}
		wantTrack := map[string]string{"new_uses": "ready", "everywhere": "deleted"}[scope]
		if rg.store.tracks[libID] != wantTrack {
			t.Fatalf("%s: sound is %q, want %q", scope, rg.store.tracks[libID], wantTrack)
		}
		// Never enabled again.
		if w := rg.call(t, http.MethodPatch, "/"+id, `{"is_active":true}`, OpSoundsManage); w.Code != 409 || soundErrCode(t, w) != "WITHDRAWN" {
			t.Fatalf("%s re-enable: %d %s", scope, w.Code, w.Body.String())
		}
		// Still readable and previewable by an admin.
		if w := rg.call(t, http.MethodGet, "/"+id, "", OpSoundsRead); w.Code != 200 || decodeSound(t, w)["status"] != "withdrawn" {
			t.Fatalf("%s get: %d %s", scope, w.Code, w.Body.String())
		}
	}
	if w := rg.call(t, http.MethodPost, "/"+uuid.NewString()+"/withdraw", `{"scope":"everywhere","reason":"licence lapsed"}`, OpSoundsManage); w.Code != 404 {
		t.Fatalf("missing: %d", w.Code)
	}
}

func TestAdminSoundPreview_AnyStatusShortLived(t *testing.T) {
	rg := newAdminSoundRig(t)
	id := rg.create(t, "")["id"].(string) // a draft
	w := rg.call(t, http.MethodGet, "/"+id+"/preview", "", OpSoundsRead)
	if w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("preview: %d %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	key := rg.store.rows[uuid.MustParse(id)].AudioURL
	if !strings.Contains(loc, key) || !strings.Contains(loc, "Signature=get") || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("Location %q, headers %v", loc, w.Header())
	}
	if ttl := rg.blobs.getTTL[key]; ttl <= 0 || ttl > 5*time.Minute {
		t.Fatalf("preview signed for %v", ttl)
	}
	if w := rg.call(t, http.MethodGet, "/"+uuid.NewString()+"/preview", "", OpSoundsRead); w.Code != 404 {
		t.Fatalf("missing: %d", w.Code)
	}
}

func TestAdminSoundList_PagesNewestFirst(t *testing.T) {
	rg := newAdminSoundRig(t)
	var ids []string
	for i := 0; i < 3; i++ {
		ids = append(ids, rg.create(t, "")["id"].(string))
		rg.store.rows[uuid.MustParse(ids[i])].CreatedAt = rg.now.Add(time.Duration(i) * time.Minute)
	}
	list := func(q string) ([]string, any) {
		w := rg.call(t, http.MethodGet, q, "", OpSoundsRead)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", q, w.Code, w.Body.String())
		}
		var env struct {
			Data []map[string]any `json:"data"`
			Meta map[string]any   `json:"meta"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if _, ok := env.Meta["next_cursor"]; !ok {
			t.Fatalf("meta lacks next_cursor: %s", w.Body.String())
		}
		var got []string
		for _, s := range env.Data {
			got = append(got, s["id"].(string))
		}
		return got, env.Meta["next_cursor"]
	}
	page, next := list("?limit=2")
	if len(page) != 2 || page[0] != ids[2] || page[1] != ids[1] || next == nil {
		t.Fatalf("page 1 %v next %v", page, next)
	}
	page, next = list("?limit=2&cursor=" + next.(string))
	if len(page) != 1 || page[0] != ids[0] || next != nil {
		t.Fatalf("page 2 %v next %v", page, next)
	}
	if page, _ := list("?status=active"); len(page) != 0 {
		t.Fatalf("drafts listed as active: %v", page)
	}
	if page, _ := list("?status=inactive&limit=500"); len(page) != 3 {
		t.Fatalf("drafts not listed as inactive: %v", page)
	}
	for _, q := range []string{"?status=gone", "?limit=0", "?limit=x", "?cursor=bm90LWEtY3Vyc29y"} {
		if w := rg.call(t, http.MethodGet, q, "", OpSoundsRead); w.Code != 400 {
			t.Fatalf("%s: %d", q, w.Code)
		}
	}
	rg.store.fail = fmt.Errorf("connection refused")
	if w := rg.call(t, http.MethodGet, "", "", OpSoundsRead); w.Code != 503 || soundErrCode(t, w) != "SOUND_LIBRARY_UNAVAILABLE" || strings.Contains(w.Body.String(), "refused") {
		t.Fatalf("store down: %d %s", w.Code, w.Body.String())
	}
}

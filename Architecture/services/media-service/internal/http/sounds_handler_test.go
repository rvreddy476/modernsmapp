package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/media-service/internal/store/blob"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Original sounds on reels over httptest, on the record fixture: the REAL
// handler, the REAL service.Sounds and service.RecordReads and a REAL
// delivery.Gate, with the store, the object store, the content authority and
// ffmpeg faked. No PostgreSQL, no ffmpeg.
//
// The properties under test:
//
//   - a video has ONE sound: a second ensure answers the first one's row and
//     extracts nothing, and two ensures at once leave one row;
//   - an asset that is not a sound source is refused before one byte of it is
//     read;
//   - a row that exists keeps its title, artist and creator;
//   - the bytes (GET and HEAD /v1/audio/:audioId/serve) go to the source
//     video's audience, and a denial is byte-for-byte the missing answer;
//   - no body carries a storage key, and usage_count is the greater counter.

// ── the fakes' ensure side ──────────────────────────────────────────────

func (f *fakeRecordStore) GetAudioTrackByMedia(_ context.Context, mediaID uuid.UUID) (*postgres.AudioTrack, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var oldest *postgres.AudioTrack
	for _, t := range f.tracks {
		if t.SourceMediaID == nil || *t.SourceMediaID != mediaID {
			continue
		}
		if oldest == nil || t.CreatedAt.Before(oldest.CreatedAt) {
			oldest = t
		}
	}
	if oldest == nil {
		return nil, pgx.ErrNoRows
	}
	cp := *oldest
	return &cp, nil
}

// InsertSoundIfAbsent is the unique index of migration 024: the first insert
// for a source is kept and every later one is a no-op.
func (f *fakeRecordStore) InsertSoundIfAbsent(_ context.Context, a *postgres.AudioTrack) (bool, error) {
	if f.fail != nil {
		return false, f.fail
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.tracks {
		if t.SourceMediaID != nil && a.SourceMediaID != nil && *t.SourceMediaID == *a.SourceMediaID {
			return false, nil
		}
	}
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	cp := *a
	cp.CreatedAt = time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	cp.UpdatedAt = cp.CreatedAt
	f.tracks[cp.ID] = &cp
	f.inserted++
	return true, nil
}

// FillSoundOrigin is the store's COALESCE: a value already there is kept.
func (f *fakeRecordStore) FillSoundOrigin(_ context.Context, id uuid.UUID, sourcePostID, creatorUserID *uuid.UUID) error {
	if f.fail != nil {
		return f.fail
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tracks[id]
	if !ok {
		return nil
	}
	changed := false
	if t.SourceReelID == nil && sourcePostID != nil {
		t.SourceReelID, changed = sourcePostID, true
	}
	if t.CreatorUserID == nil && creatorUserID != nil {
		t.CreatorUserID, changed = creatorUserID, true
	}
	if changed {
		f.filled++
	}
	return nil
}

// soundsOf lists the rows whose source is mediaID.
func (f *fakeRecordStore) soundsOf(mediaID uuid.UUID) []postgres.AudioTrack {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []postgres.AudioTrack
	for _, t := range f.tracks {
		if t.SourceMediaID != nil && *t.SourceMediaID == mediaID {
			out = append(out, *t)
		}
	}
	return out
}

func (b *fakeRecordBlobs) OpenObject(_ context.Context, key string) (io.ReadCloser, blob.ObjectInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.opened = append(b.opened, key)
	if b.openFail != nil {
		return nil, blob.ObjectInfo{}, b.openFail
	}
	body := "video:" + key
	return io.NopCloser(strings.NewReader(body)), blob.ObjectInfo{Size: int64(len(body))}, nil
}

func (b *fakeRecordBlobs) UploadObject(_ context.Context, key string, data []byte, _ string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.uploadFail != nil {
		return b.uploadFail
	}
	if b.uploaded == nil {
		b.uploaded = map[string][]byte{}
	}
	b.uploaded[key] = data
	return nil
}

// fakeSoundExtractor stands in for ffmpeg. Every call yields a different
// sound, so a row says which extraction it came from.
type fakeSoundExtractor struct {
	mu    sync.Mutex
	calls int
	// read is what each call was handed as the video.
	read []string
	err  error
	// started and release, when set, hold every call inside the extraction
	// until the test lets go, so two ensures overlap for certain.
	started chan struct{}
	release chan struct{}
}

func (e *fakeSoundExtractor) ExtractSound(ctx context.Context, video io.Reader) (*service.ExtractedSound, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	body, _ := io.ReadAll(video)
	e.mu.Lock()
	e.calls++
	n, err := e.calls, e.err
	e.read = append(e.read, string(body))
	e.mu.Unlock()
	if e.started != nil {
		e.started <- struct{}{}
		<-e.release
	}
	if err != nil {
		return nil, err
	}
	return &service.ExtractedSound{
		Audio:      []byte(fmt.Sprintf("aac-of-call-%d", n)),
		Waveform:   []byte("[0.1000,0.2000]"),
		DurationMs: 11_900 + n,
		SampleRate: 44_100,
	}, nil
}

// ── fixture ─────────────────────────────────────────────────────────────

// newEnsureFixture is the record fixture plus a reel that has no sound yet.
// Its ladder holds a rung that is not mp4 and a dub, both smaller than or
// equal to the 360p the extraction must pick.
func newEnsureFixture(t *testing.T) (*recordFixture, uuid.UUID) {
	t.Helper()
	f := newRecordFixture(t)
	reel := uuid.New()
	ms := 12_000
	key := func(name string) string { return fmt.Sprintf("user/%s/%s/%s", f.owner, reel, name) }
	f.store.media[reel] = &postgres.MediaAsset{
		ID: reel, UploaderID: f.owner, FileType: "video", MimeType: "video/mp4",
		StorageBucket: "media", StorageKey: key("original.mp4"),
		ProcessingStatus: "ready", ModerationStatus: "passed", DurationMs: &ms,
		Variants: []postgres.MediaVariant{
			{MediaAssetID: reel, Name: "thumb_150", Mime: "image/jpeg", ObjectKey: key("thumb_150.jpg")},
			{MediaAssetID: reel, Name: "720p", Mime: "video/mp4", ObjectKey: key("720p.mp4")},
			{MediaAssetID: reel, Name: "360p", Mime: "video/mp4", ObjectKey: key("360p.mp4")},
			{MediaAssetID: reel, Name: "144p", Mime: "video/webm", ObjectKey: key("144p.webm")},
			{MediaAssetID: reel, Name: "dub_hi_360p", Mime: "video/mp4", ObjectKey: key("dub/hi/360p.mp4")},
		},
	}
	return f, reel
}

func (f *recordFixture) post(path string, viewer uuid.UUID, body string, headers ...string) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(http.MethodPost, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if viewer != uuid.Nil {
		req.Header.Set("X-User-Id", viewer.String())
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

// ensure is post-service's call: the internal key, no viewer.
func (f *recordFixture) ensure(mediaID uuid.UUID, body string) *httptest.ResponseRecorder {
	return f.post("/v1/media/internal/"+mediaID.String()+"/sound", uuid.Nil, body, internalKeyHeader, recordInternalKey)
}

// extract is the uploader's own route.
func (f *recordFixture) extract(mediaID, viewer uuid.UUID, body string) *httptest.ResponseRecorder {
	return f.post("/v1/audio/extract/"+mediaID.String(), viewer, body)
}

func (f *recordFixture) serve(method string, id, viewer uuid.UUID) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/v1/audio/"+id.String()+"/serve", nil)
	if viewer != uuid.Nil {
		req.Header.Set("X-User-Id", viewer.String())
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

// soundRow decodes data as the keys it carries.
func soundRow(t *testing.T, rec *httptest.ResponseRecorder) map[string]json.RawMessage {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d %s want 200", rec.Code, rec.Body.String())
	}
	var env struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode sound: %v (%s)", err, rec.Body.String())
	}
	return env.Data
}

func keysOf(row map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(row))
	for k := range row {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// wantNothingRead: the asset was refused from its row. Nothing was opened,
// extracted, written or inserted.
func wantNothingRead(t *testing.T, what string, f *recordFixture, inserted int) {
	t.Helper()
	if len(f.blobs.opened) != 0 {
		t.Errorf("%s: objects were opened before the refusal: %v", what, f.blobs.opened)
	}
	if f.extractor.calls != 0 {
		t.Errorf("%s: %d extractions ran before the refusal", what, f.extractor.calls)
	}
	if len(f.blobs.uploaded) != 0 {
		t.Errorf("%s: objects were written: %d", what, len(f.blobs.uploaded))
	}
	if f.store.inserted != inserted {
		t.Errorf("%s: %d rows inserted, want %d", what, f.store.inserted, inserted)
	}
}

// wantNoStorageKeys: no body carries a storage path.
func wantNoStorageKeys(t *testing.T, what string, rec *httptest.ResponseRecorder) {
	t.Helper()
	for _, leak := range []string{"audio_key", "waveform_key", "audio.m4a", "waveform.json", "audio/", "user/"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Errorf("%s: the body carries %q: %s", what, leak, rec.Body.String())
		}
	}
}

// ── POST /v1/media/internal/:mediaId/sound ──────────────────────────────

// Registering the route outside the internal group fails this test.
func TestEnsureSoundNeedsTheInternalKey(t *testing.T) {
	f, reel := newEnsureFixture(t)
	path := "/v1/media/internal/" + reel.String() + "/sound"
	for name, headers := range map[string][]string{
		"no key":                            nil,
		"wrong key":                         {internalKeyHeader, "not-the-key"},
		"empty key":                         {internalKeyHeader, ""},
		"a signed-in user is not a service": {"X-User-Id", f.owner.String()},
	} {
		rec := f.post(path, uuid.Nil, `{"title":"Take"}`, headers...)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: got %d %s want 401", name, rec.Code, rec.Body.String())
		}
		wantNoStorageKeys(t, name, rec)
	}
	wantNothingRead(t, "without the key", f, 0)

	// No key configured: the route does not exist.
	f.router = f.build("")
	if rec := f.post(path, uuid.Nil, "", internalKeyHeader, recordInternalKey); rec.Code != http.StatusNotFound {
		t.Errorf("no key configured: got %d %s want 404", rec.Code, rec.Body.String())
	}
	wantNothingRead(t, "no key configured", f, 0)
}

func TestEnsureSoundWritesTheRowOfTheContract(t *testing.T) {
	f, reel := newEnsureFixture(t)
	post, author := uuid.New(), uuid.New()
	rec := f.ensure(reel, fmt.Sprintf(`{"title":"Original sound - Asha","artist":"Asha","source_post_id":%q,"creator_user_id":%q}`, post, author))
	row := soundRow(t, rec)

	rows := f.store.soundsOf(reel)
	if len(rows) != 1 {
		t.Fatalf("the reel has %d sounds, want 1", len(rows))
	}
	got := rows[0]
	if got.SourceMediaID == nil || *got.SourceMediaID != reel ||
		got.SourceReelID == nil || *got.SourceReelID != post ||
		got.CreatorUserID == nil || *got.CreatorUserID != author {
		t.Errorf("stored origin: source %v post %v creator %v", got.SourceMediaID, got.SourceReelID, got.CreatorUserID)
	}
	if got.Status != "ready" || !got.IsOriginal || got.Title != "Original sound - Asha" || got.Artist != "Asha" {
		t.Errorf("stored row: %+v", got)
	}
	if got.DurationMs != 11_901 || got.SampleRate == nil || *got.SampleRate != 44_100 {
		t.Errorf("stored measurements: %d ms, sample rate %v", got.DurationMs, got.SampleRate)
	}
	if string(f.blobs.uploaded[got.AudioKey]) != "aac-of-call-1" {
		t.Errorf("the audio at %q is %q", got.AudioKey, f.blobs.uploaded[got.AudioKey])
	}
	if got.WaveformKey == nil || string(f.blobs.uploaded[*got.WaveformKey]) != "[0.1000,0.2000]" {
		t.Errorf("the waveform was not stored: %v", got.WaveformKey)
	}
	if id := strings.Trim(string(row["id"]), `"`); id != got.ID.String() {
		t.Errorf("answered id %s, stored %s", id, got.ID)
	}
}

// The second call extracts nothing and answers the first call's row. Removing
// the look-up before the extraction fails this test.
func TestEnsureSoundSecondCallAnswersTheSameRow(t *testing.T) {
	f, reel := newEnsureFixture(t)
	first := soundRow(t, f.ensure(reel, `{"title":"Take one","artist":"Asha"}`))
	second := soundRow(t, f.ensure(reel, `{"title":"Take two","artist":"Somebody else"}`))

	if string(first["id"]) != string(second["id"]) {
		t.Errorf("second call answered %s, the first %s", second["id"], first["id"])
	}
	if f.extractor.calls != 1 || len(f.blobs.opened) != 1 {
		t.Errorf("%d extractions over %d opened objects, want 1 and 1", f.extractor.calls, len(f.blobs.opened))
	}
	if f.store.inserted != 1 || len(f.store.soundsOf(reel)) != 1 {
		t.Errorf("%d inserts, %d rows; want 1 and 1", f.store.inserted, len(f.store.soundsOf(reel)))
	}
	if string(second["title"]) != `"Take one"` || string(second["artist"]) != `"Asha"` {
		t.Errorf("the second call renamed the sound: %s by %s", second["title"], second["artist"])
	}
}

// Two ensures of one video at the same moment: both are held inside the
// extraction, so neither found a row. One insert is kept, and both callers
// are answered with it. Answering the caller's own row instead of the
// re-read one fails this test.
func TestEnsureSoundConcurrentCallsKeepOneRow(t *testing.T) {
	f, reel := newEnsureFixture(t)
	f.extractor.started = make(chan struct{}, 2)
	f.extractor.release = make(chan struct{})

	recs := make([]*httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	for i := range recs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			recs[i] = f.ensure(reel, fmt.Sprintf(`{"title":"Take %d"}`, i))
		}(i)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-f.extractor.started:
		case <-time.After(5 * time.Second):
			t.Fatal("the two calls did not both reach the extraction")
		}
	}
	close(f.extractor.release)
	wg.Wait()

	a, b := soundRow(t, recs[0]), soundRow(t, recs[1])
	if string(a["id"]) != string(b["id"]) {
		t.Errorf("the two callers hold different sounds: %s and %s", a["id"], b["id"])
	}
	for _, field := range []string{"title", "duration_ms", "created_at"} {
		if string(a[field]) != string(b[field]) {
			t.Errorf("%s differs between the two answers: %s and %s", field, a[field], b[field])
		}
	}
	rows := f.store.soundsOf(reel)
	if len(rows) != 1 || f.store.inserted != 1 {
		t.Fatalf("%d rows after %d inserts, want 1 and 1", len(rows), f.store.inserted)
	}
	if f.extractor.calls != 2 {
		t.Errorf("%d extractions; the test needs both calls to have extracted", f.extractor.calls)
	}
	if id := strings.Trim(string(a["id"]), `"`); id != rows[0].ID.String() {
		t.Errorf("answered %s, the kept row is %s", id, rows[0].ID)
	}
}

// Each refusal is decided from the row. Neutering any one guard of
// service.soundSourceRefusal fails its case here.
func TestEnsureSoundRefusesBeforeAnyDownload(t *testing.T) {
	ms := func(n int) *int { return &n }
	cases := []struct {
		name   string
		mutate func(m *postgres.MediaAsset)
		status int
		code   string
	}{
		{"too long", func(m *postgres.MediaAsset) { m.DurationMs = ms(service.MaxSoundSourceMs + 1) }, 422, "TOO_LONG"},
		{"too long, whole seconds only", func(m *postgres.MediaAsset) { m.DurationMs, m.DurationSeconds = nil, ms(301) }, 422, "TOO_LONG"},
		{"an image", func(m *postgres.MediaAsset) { m.FileType = "image" }, 422, "NOT_A_VIDEO"},
		{"a voice recording", func(m *postgres.MediaAsset) { m.FileType = "audio" }, 422, "NOT_A_VIDEO"},
		{"still processing", func(m *postgres.MediaAsset) { m.ProcessingStatus = "processing" }, 422, "NOT_READY"},
		{"failed", func(m *postgres.MediaAsset) { m.ProcessingStatus = "failed" }, 422, "NOT_READY"},
		{"rejected by moderation", func(m *postgres.MediaAsset) { m.ModerationStatus = "rejected" }, 422, "NOT_READY"},
		{"no measured duration", func(m *postgres.MediaAsset) { m.DurationMs, m.DurationSeconds = nil, nil }, 422, "NOT_READY"},
		{"a dating photo's video", func(m *postgres.MediaAsset) { m.AccessScope = postgres.AccessScopeDatingPhoto }, 404, "NOT_FOUND"},
		{"an anonymous attachment", func(m *postgres.MediaAsset) { m.AccessScope = postgres.AccessScopeAnonymous }, 404, "NOT_FOUND"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, reel := newEnsureFixture(t)
			tc.mutate(f.store.media[reel])
			rec := f.ensure(reel, `{"title":"Take"}`)
			if rec.Code != tc.status || errorCode(t, rec) != tc.code {
				t.Errorf("got %d %s want %d %s", rec.Code, rec.Body.String(), tc.status, tc.code)
			}
			wantNothingRead(t, tc.name, f, 0)
			wantNoStorageKeys(t, tc.name, rec)
		})
	}
}

func TestEnsureSoundAcceptsExactlyTheLimit(t *testing.T) {
	f, reel := newEnsureFixture(t)
	limit := service.MaxSoundSourceMs
	f.store.media[reel].DurationMs = &limit
	if rec := f.ensure(reel, ""); rec.Code != http.StatusOK {
		t.Errorf("a 300 s video: got %d %s want 200", rec.Code, rec.Body.String())
	}
}

func TestEnsureSoundMissingAssetIs404(t *testing.T) {
	f, _ := newEnsureFixture(t)
	missing := f.ensure(uuid.New(), "")
	if missing.Code != http.StatusNotFound || errorCode(t, missing) != "NOT_FOUND" {
		t.Fatalf("missing asset: got %d %s want 404 NOT_FOUND", missing.Code, missing.Body.String())
	}
	// An asset of an uploader-only scope is the same answer.
	if scoped := f.ensure(f.anonID, ""); scoped.Body.String() != missing.Body.String() {
		t.Errorf("an anonymous attachment answers %s, a missing asset %s", scoped.Body.String(), missing.Body.String())
	}
	wantNothingRead(t, "missing", f, 0)

	if rec := f.post("/v1/media/internal/not-a-uuid/sound", uuid.Nil, "", internalKeyHeader, recordInternalKey); rec.Code != http.StatusBadRequest {
		t.Errorf("bad id: got %d want 400", rec.Code)
	}
	for _, body := range []string{`{"source_post_id":"not-a-uuid"}`, `{"creator_user_id":42}`, `{"title":`} {
		if rec := f.ensure(f.publicID, body); rec.Code != http.StatusBadRequest {
			t.Errorf("body %s: got %d %s want 400", body, rec.Code, rec.Body.String())
		}
	}
}

// The smallest mp4 rung, not the original, not a rung of another container
// and not a dub, which carries another language's audio.
func TestEnsureSoundReadsTheSmallestMp4Rendition(t *testing.T) {
	f, reel := newEnsureFixture(t)
	soundRow(t, f.ensure(reel, ""))
	want := fmt.Sprintf("user/%s/%s/360p.mp4", f.owner, reel)
	if len(f.blobs.opened) != 1 || f.blobs.opened[0] != want {
		t.Fatalf("opened %v, want exactly %s", f.blobs.opened, want)
	}
	if len(f.extractor.read) != 1 || f.extractor.read[0] != "video:"+want {
		t.Errorf("the extraction was handed %q", f.extractor.read)
	}
}

func TestEnsureSoundFallsBackToTheOriginal(t *testing.T) {
	f, reel := newEnsureFixture(t)
	m := f.store.media[reel]
	m.Variants = []postgres.MediaVariant{
		{MediaAssetID: reel, Name: "thumb_150", Mime: "image/jpeg", ObjectKey: "user/x/thumb_150.jpg"},
		{MediaAssetID: reel, Name: "dub_hi_360p", Mime: "video/mp4", ObjectKey: "user/x/dub/hi/360p.mp4"},
	}
	soundRow(t, f.ensure(reel, ""))
	if len(f.blobs.opened) != 1 || f.blobs.opened[0] != m.StorageKey {
		t.Errorf("opened %v, want the original %s", f.blobs.opened, m.StorageKey)
	}
}

func TestEnsureSoundNamesAreBounded(t *testing.T) {
	f, reel := newEnsureFixture(t)
	body, _ := json.Marshal(map[string]string{
		"title":  "  " + strings.Repeat("స", 130) + "  ",
		"artist": strings.Repeat("é", 95),
	})
	row := soundRow(t, f.ensure(reel, string(body)))
	var title, artist string
	_ = json.Unmarshal(row["title"], &title)
	_ = json.Unmarshal(row["artist"], &artist)
	if title != strings.Repeat("స", 120) {
		t.Errorf("title is %d runes, want the first 120", len([]rune(title)))
	}
	if artist != strings.Repeat("é", 80) {
		t.Errorf("artist is %d runes, want the first 80", len([]rune(artist)))
	}

	f, reel = newEnsureFixture(t)
	row = soundRow(t, f.ensure(reel, `{"title":"   "}`))
	if string(row["title"]) != `"Original sound"` {
		t.Errorf("an empty title became %s", row["title"])
	}
	// With no creator named, the sound is its uploader's.
	if string(row["creator_user_id"]) != `"`+f.owner.String()+`"` || string(row["source_post_id"]) != "null" {
		t.Errorf("creator %s, source post %s", row["creator_user_id"], row["source_post_id"])
	}
}

// A row that exists is answered as it stands. Filling on "the caller named
// one" instead of "the row has none" fails this test.
func TestEnsureSoundKeepsAnExistingRowsNames(t *testing.T) {
	f, _ := newEnsureFixture(t)
	post, creator := uuid.New(), uuid.New()
	existing := f.store.tracks[f.trackPublic]
	existing.Title, existing.Artist = "Street take", "Asha"
	existing.SourceReelID, existing.CreatorUserID = &post, &creator

	row := soundRow(t, f.ensure(f.publicID, fmt.Sprintf(
		`{"title":"Renamed","artist":"Impostor","source_post_id":%q,"creator_user_id":%q}`, uuid.New(), uuid.New())))

	want := map[string]string{
		"id":              `"` + f.trackPublic.String() + `"`,
		"title":           `"Street take"`,
		"artist":          `"Asha"`,
		"source_post_id":  `"` + post.String() + `"`,
		"creator_user_id": `"` + creator.String() + `"`,
	}
	for field, value := range want {
		if string(row[field]) != value {
			t.Errorf("answered %s = %s want %s", field, row[field], value)
		}
	}
	stored := f.store.tracks[f.trackPublic]
	if stored.Title != "Street take" || stored.Artist != "Asha" || *stored.SourceReelID != post || *stored.CreatorUserID != creator {
		t.Errorf("the stored row was rewritten: %+v", stored)
	}
	if f.store.filled != 0 {
		t.Errorf("%d origin writes on a row that had its origin", f.store.filled)
	}
	wantNothingRead(t, "existing row", f, 0)
}

// A sound the uploader extracted before any post named it has no source
// post: the first post to use it is recorded, and nothing else changes.
func TestEnsureSoundFillsAnOriginTheRowLacks(t *testing.T) {
	f, _ := newEnsureFixture(t)
	existing := f.store.tracks[f.trackPublic]
	existing.Title = "Street take"
	post, author := uuid.New(), uuid.New()

	row := soundRow(t, f.ensure(f.publicID, fmt.Sprintf(`{"title":"Renamed","source_post_id":%q,"creator_user_id":%q}`, post, author)))
	if string(row["source_post_id"]) != `"`+post.String()+`"` || string(row["creator_user_id"]) != `"`+author.String()+`"` {
		t.Errorf("answered origin: post %s creator %s", row["source_post_id"], row["creator_user_id"])
	}
	stored := f.store.tracks[f.trackPublic]
	if stored.SourceReelID == nil || *stored.SourceReelID != post || stored.CreatorUserID == nil || *stored.CreatorUserID != author {
		t.Errorf("stored origin: post %v creator %v", stored.SourceReelID, stored.CreatorUserID)
	}
	if stored.Title != "Street take" || string(row["title"]) != `"Street take"` {
		t.Errorf("the title changed: stored %q answered %s", stored.Title, row["title"])
	}
	wantNothingRead(t, "existing row", f, 0)
}

// One row per source: a sound that is not playable is not replaced by a
// second one, and is not answered as if it were.
func TestEnsureSoundExistingRowThatCannotPlayIsNotReady(t *testing.T) {
	for name, mutate := range map[string]func(*postgres.AudioTrack){
		"rejected":  func(a *postgres.AudioTrack) { a.Status = "rejected" },
		"deleted":   func(a *postgres.AudioTrack) { a.Status = "deleted" },
		"no object": func(a *postgres.AudioTrack) { a.AudioKey = "" },
	} {
		f, _ := newEnsureFixture(t)
		mutate(f.store.tracks[f.trackPublic])
		rec := f.ensure(f.publicID, "")
		if rec.Code != http.StatusUnprocessableEntity || errorCode(t, rec) != "NOT_READY" {
			t.Errorf("%s: got %d %s want 422 NOT_READY", name, rec.Code, rec.Body.String())
		}
		wantNothingRead(t, name, f, 0)
	}
}

// A fault is retryable and its text stays on the server.
func TestEnsureSoundFaultIs503WithoutItsText(t *testing.T) {
	secret := `ffmpeg audio extraction failed: exit status 1 /tmp/sound-extract-123/input`
	for name, breakIt := range map[string]func(f *recordFixture){
		"ffmpeg":  func(f *recordFixture) { f.extractor.err = errors.New(secret) },
		"storage": func(f *recordFixture) { f.blobs.openFail = errors.New(secret) },
		"upload":  func(f *recordFixture) { f.blobs.uploadFail = errors.New(secret) },
		"store":   func(f *recordFixture) { f.store.fail = errors.New(secret) },
	} {
		f, reel := newEnsureFixture(t)
		breakIt(f)
		rec := f.ensure(reel, "")
		if rec.Code != http.StatusServiceUnavailable || errorCode(t, rec) != "SOUND_UNAVAILABLE" {
			t.Errorf("%s: got %d %s want 503 SOUND_UNAVAILABLE", name, rec.Code, rec.Body.String())
		}
		for _, leak := range []string{"ffmpeg", "/tmp", "exit status", "sound-extract"} {
			if strings.Contains(rec.Body.String(), leak) {
				t.Errorf("%s: the body carries %q: %s", name, leak, rec.Body.String())
			}
		}
		wantNoStorageKeys(t, name, rec)
		if f.store.inserted != 0 {
			t.Errorf("%s: a row was inserted for a sound that was not produced", name)
		}
	}
}

// A video with no audio stream is not a fault to retry.
func TestEnsureSoundVideoWithoutAudioIs422(t *testing.T) {
	f, reel := newEnsureFixture(t)
	f.extractor.err = fmt.Errorf("probe: %w", service.ErrSoundNoAudio)
	rec := f.ensure(reel, "")
	if rec.Code != http.StatusUnprocessableEntity || errorCode(t, rec) != "NO_AUDIO" {
		t.Errorf("got %d %s want 422 NO_AUDIO", rec.Code, rec.Body.String())
	}
	if f.store.inserted != 0 || len(f.blobs.uploaded) != 0 {
		t.Errorf("a silent video left %d rows and %d objects", f.store.inserted, len(f.blobs.uploaded))
	}
}

// The extraction is not the caller's to abandon: a caller that has given up
// still leaves the row for the next one. Extracting under the request's own
// context fails this test.
func TestEnsureSoundFinishesForACallerThatGaveUp(t *testing.T) {
	f, reel := newEnsureFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/v1/media/internal/"+reel.String()+"/sound", nil).WithContext(ctx)
	req.Header.Set(internalKeyHeader, recordInternalKey)
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || len(f.store.soundsOf(reel)) != 1 {
		t.Errorf("got %d %s with %d rows; want 200 and the row", rec.Code, rec.Body.String(), len(f.store.soundsOf(reel)))
	}
}

// ── POST /v1/audio/extract/:mediaId ─────────────────────────────────────

// Neutering the owner comparison in Sounds.EnsureForOwner fails this test.
func TestExtractAudioIsTheUploadersAlone(t *testing.T) {
	f, reel := newEnsureFixture(t)
	for name, viewer := range map[string]uuid.UUID{"a stranger": f.stranger, "a permitted viewer": f.viewer} {
		rec := f.extract(reel, viewer, `{"title":"Mine now"}`)
		if rec.Code != http.StatusForbidden || errorCode(t, rec) != "FORBIDDEN" {
			t.Errorf("%s: got %d %s want 403 FORBIDDEN", name, rec.Code, rec.Body.String())
		}
	}
	if rec := f.extract(reel, uuid.Nil, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("signed out: got %d want 401", rec.Code)
	}
	if rec := f.extract(uuid.New(), f.owner, ""); rec.Code != http.StatusNotFound || errorCode(t, rec) != "NOT_FOUND" {
		t.Errorf("missing asset: got %d %s want 404", rec.Code, rec.Body.String())
	}
	wantNothingRead(t, "not the uploader", f, 0)
}

// The owner's route and the internal route are one path to one row.
func TestExtractAudioSharesTheEnsurePath(t *testing.T) {
	f, reel := newEnsureFixture(t)
	mine := soundRow(t, f.extract(reel, f.owner, `{"title":"Kitchen take","artist":"Asha"}`))
	if string(mine["title"]) != `"Kitchen take"` || string(mine["creator_user_id"]) != `"`+f.owner.String()+`"` {
		t.Errorf("owner's sound: title %s creator %s", mine["title"], mine["creator_user_id"])
	}
	again := soundRow(t, f.extract(reel, f.owner, `{"title":"Renamed"}`))
	post := uuid.New()
	internal := soundRow(t, f.ensure(reel, fmt.Sprintf(`{"title":"Original sound - Asha","source_post_id":%q}`, post)))
	for name, row := range map[string]map[string]json.RawMessage{"the owner again": again, "post-service": internal} {
		if string(row["id"]) != string(mine["id"]) || string(row["title"]) != `"Kitchen take"` {
			t.Errorf("%s: got %s %s, want the owner's row %s", name, row["id"], row["title"], mine["id"])
		}
	}
	if f.extractor.calls != 1 || f.store.inserted != 1 {
		t.Errorf("%d extractions and %d inserts over three calls, want 1 and 1", f.extractor.calls, f.store.inserted)
	}

	f, reel = newEnsureFixture(t)
	long := service.MaxSoundSourceMs + 1
	f.store.media[reel].DurationMs = &long
	if rec := f.extract(reel, f.owner, ""); rec.Code != http.StatusUnprocessableEntity || errorCode(t, rec) != "TOO_LONG" {
		t.Errorf("the owner's long video: got %d %s want 422 TOO_LONG", rec.Code, rec.Body.String())
	}
	wantNothingRead(t, "too long, owner", f, 0)
}

// ── the Sound shape ─────────────────────────────────────────────────────

// soundKeys is every key a sound carries: contract 1.4, and the fields the
// row carried before it (genre and sample_rate when the row has them).
var soundKeys = []string{
	"artist", "created_at", "creator_user_id", "duration_ms", "id", "is_original", "license_type",
	"sample_rate", "source_media_id", "source_post_id", "source_reel_id", "status", "title",
	"updated_at", "usage_count",
}

// Restoring the json tag of AudioKey or WaveformKey fails this test.
func TestSoundRowCarriesNoStorageKey(t *testing.T) {
	f, reel := newEnsureFixture(t)
	post := uuid.New()
	ensured := f.ensure(reel, fmt.Sprintf(`{"title":"Kitchen take","artist":"Asha","source_post_id":%q}`, post))
	row := soundRow(t, ensured)
	if got := keysOf(row); strings.Join(got, ",") != strings.Join(soundKeys, ",") {
		t.Errorf("keys of a sound:\n got %v\nwant %v", got, soundKeys)
	}
	if string(row["source_post_id"]) != string(row["source_reel_id"]) || string(row["source_post_id"]) != `"`+post.String()+`"` {
		t.Errorf("source_post_id %s, source_reel_id %s, want both %s", row["source_post_id"], row["source_reel_id"], post)
	}
	t.Logf("a sound as the handler emits it: %s", strings.TrimSpace(ensured.Body.String()))

	id := uuid.MustParse(strings.Trim(string(row["id"]), `"`))
	f.store.listed = []uuid.UUID{id, f.trackPublic, f.trackPrivate}
	waveform := "audio/x/y/waveform.json"
	f.store.tracks[f.trackPublic].WaveformKey = &waveform
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"ensure":       ensured,
		"ensure again": f.ensure(reel, ""),
		"extract":      f.extract(reel, f.owner, ""),
		"one sound":    f.sound(id, f.owner),
		"public sound": f.sound(f.trackPublic, uuid.Nil),
		"trending":     f.get("/v1/audio/trending", f.owner),
		"search":       f.get("/v1/audio/search?q=take", f.owner),
		"url":          f.audioURL(f.trackPublic, uuid.Nil),
	} {
		if rec.Code != http.StatusOK {
			t.Errorf("%s: got %d %s want 200", name, rec.Code, rec.Body.String())
		}
		for _, leak := range []string{"audio_key", "waveform_key", "waveform.json"} {
			if strings.Contains(rec.Body.String(), leak) {
				t.Errorf("%s: the body carries %q: %s", name, leak, rec.Body.String())
			}
		}
		if name != "url" && strings.Contains(rec.Body.String(), "audio.m4a") {
			t.Errorf("%s: the body carries the audio's storage path: %s", name, rec.Body.String())
		}
	}
}

// Returning usage_count alone from AudioTrack.WireUsageCount fails this test.
func TestSoundUsageCountIsTheGreaterCounter(t *testing.T) {
	f, _ := newSoundFixture(t)
	f.store.tracks[f.trackPublic].UsageCount, f.store.tracks[f.trackPublic].UseCount = 1, 3
	f.store.tracks[f.trackPrivate].UsageCount, f.store.tracks[f.trackPrivate].UseCount = 7, 2

	if got := dataField(t, f.sound(f.trackPublic, uuid.Nil), "usage_count"); got != "3" {
		t.Errorf("usage_count 1, use_count 3: answered %s want 3", got)
	}
	if got := dataField(t, f.sound(f.trackPrivate, f.owner), "usage_count"); got != "7" {
		t.Errorf("usage_count 7, use_count 2: answered %s want 7", got)
	}
	if got := dataField(t, f.ensure(f.publicID, ""), "usage_count"); got != "3" {
		t.Errorf("ensure of an existing sound: answered %s want 3", got)
	}
	var env struct {
		Data struct {
			Tracks []struct {
				ID         uuid.UUID `json:"id"`
				UsageCount int       `json:"usage_count"`
			} `json:"tracks"`
		} `json:"data"`
	}
	rec := f.get("/v1/audio/trending", f.owner)
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode trending: %v", err)
	}
	counts := map[uuid.UUID]int{}
	for _, row := range env.Data.Tracks {
		counts[row.ID] = row.UsageCount
	}
	if counts[f.trackPublic] != 3 || counts[f.trackPrivate] != 7 {
		t.Errorf("trending counts %v, want %s: 3 and %s: 7", counts, f.trackPublic, f.trackPrivate)
	}
	if strings.Contains(rec.Body.String(), "use_count") {
		t.Errorf("the second counter is on the wire: %s", rec.Body.String())
	}
}

// ── GET and HEAD /v1/audio/:audioId/serve ───────────────────────────────

func wantSoundRedirect(t *testing.T, what string, rec *httptest.ResponseRecorder, key string) {
	t.Helper()
	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("%s: got %d %s want 307", what, rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "https://signed.example/"+key+"?") {
		t.Errorf("%s: Location %q is not the presigned audio %s", what, loc, key)
	}
	if got := rec.Header().Get("Cache-Control"); got != "private, max-age=60" {
		t.Errorf("%s: Cache-Control = %q", what, got)
	}
	if got := rec.Header().Get("Vary"); got != "Cookie, Authorization, X-User-Id" {
		t.Errorf("%s: Vary = %q", what, got)
	}
}

func TestSoundServeRedirectsAnAdmittedViewer(t *testing.T) {
	f := newRecordFixture(t)
	private, public := f.store.tracks[f.trackPrivate].AudioKey, f.store.tracks[f.trackPublic].AudioKey
	wantSoundRedirect(t, "permitted viewer", f.serve(http.MethodGet, f.trackPrivate, f.viewer), private)
	wantSoundRedirect(t, "signed out on a public video", f.serve(http.MethodGet, f.trackPublic, uuid.Nil), public)

	f.authz.err = delivery.ErrDeliveryUnresolved
	wantSoundRedirect(t, "the uploader, authority down", f.serve(http.MethodGet, f.trackPrivate, f.owner), private)
	if f.authz.calls != 2 {
		t.Errorf("authority asked %d times, want 2 (never for the uploader)", f.authz.calls)
	}
}

// Neutering RecordReads.admitToTrack fails this test and the HEAD's.
func TestSoundServeDeniedIsTheMissingAnswer(t *testing.T) {
	f := newRecordFixture(t)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		missing := func(viewer uuid.UUID) *httptest.ResponseRecorder { return f.serve(method, uuid.New(), viewer) }
		wantSoundDenied(t, method+" signed out on private", f.serve(method, f.trackPrivate, uuid.Nil), missing(uuid.Nil))
		wantSoundDenied(t, method+" stranger on private", f.serve(method, f.trackPrivate, f.stranger), missing(f.stranger))
		wantSoundDenied(t, method+" no source", f.serve(method, f.trackOrphan, f.owner), missing(f.owner))

		denied := f.serve(method, f.trackPrivate, f.stranger)
		if msg := dataFieldOfError(t, denied); msg != "Audio track not found" {
			t.Errorf("%s: the denial says %q, want the missing sound's own words", method, msg)
		}
		for _, name := range []string{"Location", "Accept-Ranges", "Cache-Control"} {
			if v := denied.Header().Get(name); v != "" {
				t.Errorf("%s: a denial carries %s: %q", method, name, v)
			}
		}
	}
	if len(f.blobs.signed) != 0 {
		t.Errorf("a URL was signed for a denied viewer: %v", f.blobs.signed)
	}
}

func dataFieldOfError(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v (%s)", err, rec.Body.String())
	}
	return env.Error.Message
}

// A row with no audio object (one post-service's shape wrote) has nothing to
// redirect to. Removing the empty-key guard of AudioTrackURLForViewer fails
// this test.
func TestSoundWithoutAnObjectIsTheMissingAnswer(t *testing.T) {
	f := newRecordFixture(t)
	f.store.tracks[f.trackPublic].AudioKey = ""
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		wantDeniedLikeMissing(t, method, f.serve(method, f.trackPublic, f.viewer), f.serve(method, uuid.New(), f.viewer))
	}
	wantDeniedLikeMissing(t, "url", f.audioURL(f.trackPublic, f.viewer), f.audioURL(uuid.New(), f.viewer))
	if len(f.blobs.signed) != 0 {
		t.Errorf("an empty key was signed: %v", f.blobs.signed)
	}
}

func TestSoundServeUnresolvedIs503(t *testing.T) {
	f := newRecordFixture(t)
	f.authz.err = delivery.ErrDeliveryUnresolved
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		rec := f.serve(method, f.trackPublic, f.viewer)
		if rec.Code != http.StatusServiceUnavailable || errorCode(t, rec) != "DEPENDENCY_UNAVAILABLE" {
			t.Errorf("%s, authority down: got %d %s want 503", method, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Location") != "" {
			t.Errorf("%s: redirected while the decision was unresolved", method)
		}
	}
	if len(f.blobs.signed) != 0 {
		t.Errorf("a URL was signed while the decision was unresolved: %v", f.blobs.signed)
	}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		req := httptest.NewRequest(method, "/v1/audio/not-a-uuid/serve", nil)
		rec := httptest.NewRecorder()
		f.router.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s, bad id: got %d want 400", method, rec.Code)
		}
	}
}

// Answering the HEAD with the GET's redirect fails this test.
func TestSoundHeadHasNoBodyAndNoLocation(t *testing.T) {
	f := newRecordFixture(t)
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"permitted viewer":             f.serve(http.MethodHead, f.trackPrivate, f.viewer),
		"signed out on a public video": f.serve(http.MethodHead, f.trackPublic, uuid.Nil),
	} {
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: got %d %s want 200", name, rec.Code, rec.Body.String())
		}
		if rec.Body.Len() != 0 {
			t.Errorf("%s: a HEAD carried a %d-byte body: %q", name, rec.Body.Len(), rec.Body.String())
		}
		if loc := rec.Header().Get("Location"); loc != "" {
			t.Errorf("%s: a HEAD redirected to %q", name, loc)
		}
		want := map[string]string{
			"Content-Type":  "audio/mp4",
			"Accept-Ranges": "bytes",
			"Cache-Control": "private, max-age=60",
			"Vary":          "Cookie, Authorization, X-User-Id",
			// The row records no size and the object is never opened.
			"Content-Length": "",
		}
		for header, value := range want {
			if got := rec.Header().Get(header); got != value {
				t.Errorf("%s: %s = %q want %q", name, header, got, value)
			}
		}
		seen := fmt.Sprint(rec.Header())
		for _, leak := range []string{"signed.example", "audio/", ".m4a"} {
			if strings.Contains(strings.ReplaceAll(seen, "audio/mp4", ""), leak) {
				t.Errorf("%s: the headers carry %q: %s", name, leak, seen)
			}
		}
	}
	if len(f.blobs.opened) != 0 {
		t.Errorf("a HEAD opened %v", f.blobs.opened)
	}
}

// Over a real connection: what an <audio> element and a validator see.
func TestSoundServeOverTheWire(t *testing.T) {
	f := newRecordFixture(t)
	srv := httptest.NewServer(f.router)
	defer srv.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	path := srv.URL + "/v1/audio/" + f.trackPublic.String() + "/serve"

	resp, err := client.Get(path)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTemporaryRedirect || !strings.HasPrefix(resp.Header.Get("Location"), "https://signed.example/audio/") {
		t.Errorf("GET: got %d to %q, want 307 to the presigned audio", resp.StatusCode, resp.Header.Get("Location"))
	}

	resp, err = client.Head(path)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || len(body) != 0 || resp.Header.Get("Location") != "" || resp.Header.Get("Content-Type") != "audio/mp4" {
		t.Errorf("HEAD: got %d, %d body bytes, Location %q, Content-Type %q", resp.StatusCode, len(body), resp.Header.Get("Location"), resp.Header.Get("Content-Type"))
	}

	resp, err = client.Head(srv.URL + "/v1/audio/" + f.trackPrivate.String() + "/serve")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("HEAD of a private video's sound, signed out: got %d want 404", resp.StatusCode)
	}
}

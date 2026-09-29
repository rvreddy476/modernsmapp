package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/post-service/internal/store/scylla"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Original sounds (2026-09-29) through the REAL router and service:
// POST /v1/posts/:postId/sound and GET /v1/posts/by-sound/:soundId over
// in-memory stores, with media-service stood up as an httptest server. The
// three golden fixtures under testdata/contracts/sounds/ are what these
// handlers answer, byte for byte; the web copies them.
//
//	UPDATE_CONTRACTS=1 go test ./internal/http -run TestSoundContracts
//
// regenerates them from the handlers; review the diff before committing.
// The rules are proved in service/sounds_test.go and audio_test.go, the SQL
// in service/sounds_integration_test.go.

var (
	sxSound       = uuid.MustParse("cccccccc-cccc-4ccc-8ccc-cccccccccccc")
	sxOrigin      = uuid.MustParse("dddddddd-dddd-4ddd-8ddd-dddddddddddd")
	sxCreator     = uuid.MustParse("eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee")
	sxOriginMedia = uuid.MustParse("ffffffff-ffff-4fff-8fff-ffffffffffff")
	sxReelA       = uuid.MustParse("a1a1a1a1-a1a1-4a1a-8a1a-a1a1a1a1a1a1")
	sxReelAMedia  = uuid.MustParse("a2a2a2a2-a2a2-4a2a-8a2a-a2a2a2a2a2a2")
	sxReelB       = uuid.MustParse("b1b1b1b1-b1b1-4b1b-8b1b-b1b1b1b1b1b1")
	sxReelBMedia  = uuid.MustParse("b2b2b2b2-b2b2-4b2b-8b2b-b2b2b2b2b2b2")
	sxPrivate     = uuid.MustParse("c1c1c1c1-c1c1-4c1c-8c1c-c1c1c1c1c1c1") // a sound over a video only its creator may watch
	sxPrivMedia   = uuid.MustParse("c2c2c2c2-c2c2-4c2c-8c2c-c2c2c2c2c2c2")
)

const soundTestKey = "sound-test-internal-key"

type soundRouteStore struct {
	mu      sync.Mutex
	tracks  map[uuid.UUID]*postgres.AudioTrack
	posts   map[uuid.UUID]*postgres.Post
	sources map[uuid.UUID][]postgres.SoundSourcePost
	listed  []soundRouteList
}

type soundRouteList struct {
	sound   uuid.UUID
	exclude *uuid.UUID
	limit   int
	cursor  string
}

func (s *soundRouteStore) GetAudioTracksByIDs(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]*postgres.AudioTrack, error) {
	out := map[uuid.UUID]*postgres.AudioTrack{}
	for _, id := range ids {
		if t, ok := s.tracks[id]; ok {
			cp := *t
			out[id] = &cp
		}
	}
	return out, nil
}

func (s *soundRouteStore) SoundSourcePosts(_ context.Context, sourcePostID, _ *uuid.UUID) ([]postgres.SoundSourcePost, error) {
	if sourcePostID == nil {
		return nil, nil
	}
	return s.sources[*sourcePostID], nil
}

func (s *soundRouteStore) GetPost(_ context.Context, id uuid.UUID) (*postgres.Post, error) {
	p, ok := s.posts[id]
	if !ok {
		return nil, nil
	}
	cp := *p
	cp.Media = append([]postgres.PostMedia(nil), p.Media...)
	return &cp, nil
}

// ListPostsBySound keeps the store's contract in memory: the posts that
// play the sound, newest first by (created_at, id), minus `exclude`, after
// the cursor, one page at a time.
func (s *soundRouteStore) ListPostsBySound(_ context.Context, soundID uuid.UUID, exclude *uuid.UUID, limit int, cursor string) ([]postgres.Post, string, error) {
	s.mu.Lock()
	s.listed = append(s.listed, soundRouteList{soundID, exclude, limit, cursor})
	s.mu.Unlock()
	var rows []postgres.Post
	for _, p := range s.posts {
		if p.AudioTrackID == nil || *p.AudioTrackID != soundID || (exclude != nil && p.ID == *exclude) {
			continue
		}
		cp := *p
		cp.Media = append([]postgres.PostMedia(nil), p.Media...)
		rows = append(rows, cp)
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].CreatedAt.After(rows[j].CreatedAt)
		}
		return rows[i].ID.String() > rows[j].ID.String()
	})
	if cursor != "" {
		at, id, err := postgres.ParseSoundPostsCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		kept := rows[:0]
		for _, p := range rows {
			if p.CreatedAt.Before(at) || (p.CreatedAt.Equal(at) && p.ID.String() < id.String()) {
				kept = append(kept, p)
			}
		}
		rows = kept
	}
	next := ""
	if len(rows) > limit {
		last := rows[limit-1]
		next = last.CreatedAt.Format(time.RFC3339Nano) + "_" + last.ID.String()
		rows = rows[:limit]
	}
	return rows, next, nil
}

type soundRouteStates map[uuid.UUID]postgres.MediaOwnership

func (s soundRouteStates) BatchGetMediaOwnership(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]postgres.MediaOwnership, error) {
	out := map[uuid.UUID]postgres.MediaOwnership{}
	for _, id := range ids {
		if m, ok := s[id]; ok {
			out[id] = m
		}
	}
	return out, nil
}

// soundRouteMedia is media-service's sound route (contract 1.1).
type soundRouteMedia struct {
	mu      sync.Mutex
	status  int
	body    string
	calls   int
	method  string
	path    string
	headers http.Header
	got     []byte
}

func (m *soundRouteMedia) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	m.method, m.path, m.headers = r.Method, r.URL.Path, r.Header.Clone()
	m.got, _ = io.ReadAll(r.Body)
	w.Header().Set("Content-Type", "application/json")
	if m.status != 0 && m.status != http.StatusOK {
		w.WriteHeader(m.status)
		_, _ = io.WriteString(w, m.body)
		return
	}
	var in struct {
		Title         string    `json:"title"`
		Artist        string    `json:"artist"`
		SourcePostID  uuid.UUID `json:"source_post_id"`
		CreatorUserID uuid.UUID `json:"creator_user_id"`
	}
	_ = json.Unmarshal(m.got, &in)
	media := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/media/internal/"), "/sound")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
		"id": sxSound, "title": in.Title, "artist": in.Artist, "duration_ms": 28400, "status": "ready", "is_original": true,
		"usage_count": 0, "source_media_id": media, "source_post_id": in.SourcePostID, "source_reel_id": in.SourcePostID,
		"creator_user_id": in.CreatorUserID, "created_at": fxTime,
	}})
}

type soundRig struct {
	router      *gin.Engine
	store       *soundRouteStore
	states      soundRouteStates
	media       *soundRouteMedia
	mediaServer *httptest.Server
	hidden      *feedRouteHidden
	unreadable  map[uuid.UUID]bool // posts the read gate refuses
	audienceErr error
	asked       []uuid.UUID // the viewer of every audience question
}

// newSoundRig serves the fixtures' world: the sound taken from Asha's reel
// (sxOrigin), two reels that play it (A newer than B), and a sound over a
// video only its creator may watch.
func newSoundRig(t *testing.T) *soundRig {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := &soundRig{
		store: &soundRouteStore{tracks: map[uuid.UUID]*postgres.AudioTrack{}, posts: map[uuid.UUID]*postgres.Post{},
			sources: map[uuid.UUID][]postgres.SoundSourcePost{}},
		states: soundRouteStates{}, media: &soundRouteMedia{},
		hidden: &feedRouteHidden{hidden: map[uuid.UUID]bool{}}, unreadable: map[uuid.UUID]bool{},
	}
	r.store.tracks[sxSound] = &postgres.AudioTrack{ID: sxSound, Title: "Original sound - Asha", Artist: "Asha", DurationMs: 28400,
		MediaID: &sxOriginMedia, SourcePostID: &sxOrigin, Status: "ready", UseCount: 3, IsPublic: true, CreatorUserID: &sxCreator}
	r.store.tracks[sxPrivate] = &postgres.AudioTrack{ID: sxPrivate, Title: "Kept", DurationMs: 9000,
		MediaID: &sxPrivMedia, Status: "ready", IsPublic: true, CreatorUserID: &sxCreator}

	reel := func(id, author, media uuid.UUID, title string, age time.Duration, sound *uuid.UUID, startMs int) {
		at := fxTime.Add(-age)
		p := &postgres.Post{ID: id, AuthorID: author, Text: title + " #reels", Visibility: "public", ContentType: "flick",
			PostType: "video", AppOrigin: "posttube", ReviewStatus: "approved", Title: title, Language: "en",
			AllowEmbedding: true, PublishToFeed: true, RemixSetting: "allow", OriginalAudioVol: 1, OverlayAudioVol: 1,
			AllowDownload: true, ContentTypeExplicit: true, Source: "upload", DefaultCommentSort: "top",
			Hashtags: []string{"reels"}, CreatedAt: at, UpdatedAt: at, PublishedAt: &at,
			Media: []postgres.PostMedia{{MediaID: media, Kind: "video", Position: 0}}}
		if sound != nil {
			p.AudioTrackID, p.AudioStartMs = sound, &startMs
			p.OriginalAudioVol, p.OverlayAudioVol = 0.2, 1
		}
		r.store.posts[id] = p
		r.states[media] = postgres.MediaOwnership{UploaderID: author, Kind: "video", ProcessingStatus: "ready", ModerationStatus: "passed",
			DurationMs: 28400, HasHLS: true}
	}
	reel(sxOrigin, sxCreator, sxOriginMedia, "Monsoon walk", 72*time.Hour, nil, 0)
	reel(sxReelA, fxAuthor, sxReelAMedia, "Monsoon walk, my take", time.Hour, &sxSound, 1500)
	reel(sxReelB, fxViewer, sxReelBMedia, "Rain on the roof", 26*time.Hour, &sxSound, 0)
	r.store.sources[sxOrigin] = []postgres.SoundSourcePost{{ID: sxOrigin, AuthorID: sxCreator, RemixSetting: "allow"}}

	r.mediaServer = httptest.NewServer(r.media)
	t.Cleanup(r.mediaServer.Close)
	chans := &esChannels{subs: map[[2]uuid.UUID]bool{}, byUser: map[uuid.UUID]*postgres.Channel{
		sxCreator: {ID: uuid.New(), UserID: sxCreator, Name: "Asha", Handle: "asha"},
	}}
	deps := service.HandlerTestDeps{
		SoundReads: r.store, MediaStates: r.states, Channels: chans, HiddenAuthors: r.hidden,
		MediaServiceURL: r.mediaServer.URL, InternalServiceKey: soundTestKey,
		Now: func() time.Time { return fxTime },
		SoundCounts: func(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]*scylla.Counts, error) {
			out := map[uuid.UUID]*scylla.Counts{}
			for _, id := range ids {
				out[id] = &scylla.Counts{Likes: 12, Comments: 3}
			}
			return out, nil
		},
		SoundAudience: func(_ context.Context, viewer uuid.UUID, media []uuid.UUID) (map[uuid.UUID]bool, error) {
			r.asked = append(r.asked, viewer)
			if r.audienceErr != nil {
				return nil, r.audienceErr
			}
			out := map[uuid.UUID]bool{}
			for _, m := range media {
				out[m] = m == sxOriginMedia || (m == sxPrivMedia && viewer == sxCreator)
			}
			return out, nil
		},
		ReadGate: func(_ context.Context, postID uuid.UUID, _ *uuid.UUID) error {
			if r.unreadable[postID] || r.store.posts[postID] == nil {
				return service.ErrPostNotVisible
			}
			return nil
		},
	}
	r.router = gin.New()
	New(service.NewForHandlerTests(deps), nil).RegisterRoutes(r.router)
	return r
}

func (r *soundRig) do(method, path string, viewer *uuid.UUID) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if viewer != nil {
		req.Header.Set("X-User-Id", viewer.String())
	}
	w := httptest.NewRecorder()
	r.router.ServeHTTP(w, req)
	return w
}

func (r *soundRig) bySound(sound uuid.UUID, query string, viewer *uuid.UUID) *httptest.ResponseRecorder {
	return r.do(http.MethodGet, "/v1/posts/by-sound/"+sound.String()+query, viewer)
}

func (r *soundRig) use(post uuid.UUID, viewer *uuid.UUID) *httptest.ResponseRecorder {
	return r.do(http.MethodPost, "/v1/posts/"+post.String()+"/sound", viewer)
}

type soundEnvelope struct {
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Meta *struct {
		NextCursor string `json:"next_cursor"`
	} `json:"meta"`
}

func decodeSound(t *testing.T, w *httptest.ResponseRecorder) soundEnvelope {
	t.Helper()
	var env soundEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("status=%d body=%s: %v", w.Code, w.Body.String(), err)
	}
	return env
}

func wantSoundError(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	env := decodeSound(t, w)
	if w.Code != status || env.Error == nil || env.Error.Code != code {
		t.Fatalf("status=%d body=%s, want %d %s", w.Code, w.Body.String(), status, code)
	}
}

type bySoundData struct {
	Sound  map[string]json.RawMessage   `json:"sound"`
	Origin map[string]json.RawMessage   `json:"origin"`
	Items  []map[string]json.RawMessage `json:"items"`
}

func decodeBySound(t *testing.T, w *httptest.ResponseRecorder) (bySoundData, string) {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	env := decodeSound(t, w)
	var data bySoundData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatal(err)
	}
	next := ""
	if env.Meta != nil {
		next = env.Meta.NextCursor
	}
	return data, next
}

func idOf(t *testing.T, row map[string]json.RawMessage) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := json.Unmarshal(row["id"], &id); err != nil {
		t.Fatalf("row has no id: %v", row)
	}
	return id
}

func TestSoundRoutesRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	(&Handler{}).RegisterRoutes(r)
	want := map[string]bool{"GET /v1/posts/by-sound/:soundId": false, "POST /v1/posts/:postId/sound": false}
	for _, info := range r.Routes() {
		if _, ok := want[info.Method+" "+info.Path]; ok {
			want[info.Method+" "+info.Path] = true
		}
	}
	for route, found := range want {
		if !found {
			t.Fatalf("%s is not registered", route)
		}
	}
}

// ---- the golden fixtures ----

// soundContracts asks the real handlers for the three documents.
func soundContracts(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	r := newSoundRig(t)
	page := r.bySound(sxSound, "", &fxViewer)
	if page.Code != http.StatusOK {
		t.Fatalf("by-sound: status=%d body=%s", page.Code, page.Body.String())
	}
	bySound := decodeSound(t, page).Data
	var rows struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(bySound, &rows); err != nil || len(rows.Items) != 2 {
		t.Fatalf("by-sound items: %v %s", err, bySound)
	}
	used := r.use(sxOrigin, &fxViewer)
	if used.Code != http.StatusOK {
		t.Fatalf("use sound: status=%d body=%s", used.Code, used.Body.String())
	}
	return map[string]json.RawMessage{
		"post_with_sound.json": rows.Items[0],
		"use_sound.json":       decodeSound(t, used).Data,
		"by_sound.json":        bySound,
	}
}

func indentSound(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		t.Fatal(err)
	}
	return append(buf.Bytes(), '\n')
}

func TestSoundContracts(t *testing.T) {
	dir := filepath.Join("testdata", "contracts", "sounds")
	update := os.Getenv("UPDATE_CONTRACTS") == "1"
	if update {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	contracts := soundContracts(t)
	if len(contracts) != 3 {
		t.Fatalf("%d documents", len(contracts))
	}
	for name, raw := range contracts {
		t.Run(name, func(t *testing.T) {
			got := indentSound(t, raw)
			path := filepath.Join(dir, name)
			if update {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read fixture: %v (UPDATE_CONTRACTS=1 to generate)", err)
			}
			if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), got) {
				t.Fatalf("the route does not answer fixture %s.\n--- want\n%s\n--- got\n%s", name, want, got)
			}
		})
	}
	// Nothing else lives in the directory: a fixture no test asserts is a lie.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if _, ok := contracts[e.Name()]; !ok {
			t.Fatalf("testdata/contracts/sounds/%s is asserted by no test", e.Name())
		}
	}
}

func sortedKeys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func wantKeys(t *testing.T, name string, got map[string]json.RawMessage, want ...string) {
	t.Helper()
	sort.Strings(want)
	if keys := sortedKeys(got); !reflect.DeepEqual(keys, want) {
		t.Fatalf("%s keys = %v\nwant %v", name, keys, want)
	}
}

var soundKeys = []string{"id", "title", "artist", "duration_ms", "start_ms", "use_count", "source_post_id", "creator_user_id"}

// postKeys is every key of a listed reel that plays no added sound;
// postWithSoundKeys adds the three the contract adds.
var postKeys = []string{"id", "author_id", "text", "visibility", "content_type", "is_pinned", "no_comments", "no_likes",
	"hashtags", "post_type", "app_origin", "share_to_postbook", "review_status", "title", "language", "paid_promotion",
	"altered_content", "is_made_for_kids", "allow_embedding", "publish_to_feed", "remix_setting", "original_audio_volume",
	"overlay_audio_volume", "hide_share", "allow_download", "age_restricted", "hide_like_count", "default_comment_sort",
	"related_post_id", "content_type_explicit", "source", "distribution_rev", "created_at", "updated_at", "published_at",
	"is_scheduled", "media", "is_processing", "counts", "view_count", "has_reacted", "is_bookmarked", "repost_count",
	"has_reposted", "is_repostable", "viewer_disliked", "viewer_queued", "chapters"}

var postWithSoundKeys = append(append([]string(nil), postKeys...), "audio_track_id", "audio_start_ms", "sound")

// Every key the contract pins is on the wire and nothing else, in the
// handlers' answers and in the fixtures alike.
func TestSoundWireKeysArePinned(t *testing.T) {
	check := func(t *testing.T, contracts map[string]json.RawMessage) {
		t.Helper()
		var page bySoundData
		var top, post, used map[string]json.RawMessage
		if err := json.Unmarshal(contracts["by_sound.json"], &page); err != nil {
			t.Fatal(err)
		}
		_ = json.Unmarshal(contracts["by_sound.json"], &top)
		_ = json.Unmarshal(contracts["post_with_sound.json"], &post)
		_ = json.Unmarshal(contracts["use_sound.json"], &used)

		wantKeys(t, "by-sound", top, "sound", "origin", "items")
		wantKeys(t, "by-sound sound", page.Sound, soundKeys...)
		wantKeys(t, "origin", page.Origin, postKeys...)
		if len(page.Items) != 2 {
			t.Fatalf("%d items", len(page.Items))
		}
		for i, item := range page.Items {
			wantKeys(t, "item", item, postWithSoundKeys...)
			var sound map[string]json.RawMessage
			if err := json.Unmarshal(item["sound"], &sound); err != nil {
				t.Fatal(err)
			}
			wantKeys(t, "item sound", sound, soundKeys...)
			_ = i
		}
		wantKeys(t, "post with sound", post, postWithSoundKeys...)
		wantKeys(t, "use sound", used, "sound")
		var made map[string]json.RawMessage
		if err := json.Unmarshal(used["sound"], &made); err != nil {
			t.Fatal(err)
		}
		wantKeys(t, "use sound sound", made, soundKeys...)
	}

	t.Run("the handlers", func(t *testing.T) { check(t, soundContracts(t)) })
	t.Run("the fixtures", func(t *testing.T) {
		fixtures := map[string]json.RawMessage{}
		for _, name := range []string{"post_with_sound.json", "use_sound.json", "by_sound.json"} {
			raw, err := os.ReadFile(filepath.Join("testdata", "contracts", "sounds", name))
			if err != nil {
				t.Fatal(err)
			}
			fixtures[name] = raw
		}
		check(t, fixtures)
	})
}

// No storage path and no presigned URL is ever in a post body.
func TestSoundAnswersCarryNoStoragePath(t *testing.T) {
	for name, raw := range soundContracts(t) {
		for _, needle := range []string{"audio_key", "waveform_key", "object_key", "X-Amz", "Signature=", "s3://", "http://", "https://"} {
			if strings.Contains(string(raw), needle) {
				t.Fatalf("%s carries %q: %s", name, needle, raw)
			}
		}
	}
}

// ---- GET /v1/posts/by-sound/:soundId ----

func TestBySoundValues(t *testing.T) {
	r := newSoundRig(t)
	data, next := decodeBySound(t, r.bySound(sxSound, "", nil))
	if next != "" {
		t.Fatalf("next_cursor = %q on a page that holds everything", next)
	}
	var sound service.PostSound
	raw, _ := json.Marshal(data.Sound)
	if err := json.Unmarshal(raw, &sound); err != nil {
		t.Fatal(err)
	}
	if sound.ID != sxSound || sound.Title != "Original sound - Asha" || sound.Artist != "Asha" || sound.DurationMs != 28400 ||
		sound.StartMs != 0 || sound.UseCount != 3 || sound.SourcePostID == nil || *sound.SourcePostID != sxOrigin ||
		sound.CreatorUserID == nil || *sound.CreatorUserID != sxCreator {
		t.Fatalf("sound = %+v", sound)
	}
	if idOf(t, data.Origin) != sxOrigin {
		t.Fatalf("origin = %v", data.Origin)
	}
	if len(data.Items) != 2 || idOf(t, data.Items[0]) != sxReelA || idOf(t, data.Items[1]) != sxReelB {
		t.Fatalf("items are not the two reels, newest first: %v", data.Items)
	}
	// Each row carries its own start into the sound.
	for i, want := range []string{"1500", "0"} {
		var rowSound service.PostSound
		if err := json.Unmarshal(data.Items[i]["sound"], &rowSound); err != nil {
			t.Fatal(err)
		}
		if string(data.Items[i]["audio_start_ms"]) != want || rowSound.ID != sxSound || string(data.Items[i]["audio_start_ms"]) != jsonInt(rowSound.StartMs) {
			t.Fatalf("item %d: audio_start_ms=%s sound=%+v", i, data.Items[i]["audio_start_ms"], rowSound)
		}
	}
	// Signed out, the audience was asked as the nil viewer.
	for _, viewer := range r.asked {
		if viewer != uuid.Nil {
			t.Fatalf("a signed-out request asked as %s", viewer)
		}
	}
}

func jsonInt(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// A sound the viewer may not hear is answered exactly like one that does
// not exist.
func TestBySoundDenialIsTheMissingAnswer(t *testing.T) {
	r := newSoundRig(t)
	stranger := uuid.New()
	missing := r.bySound(uuid.New(), "", &stranger)
	wantSoundError(t, missing, http.StatusNotFound, "NOT_FOUND")

	for name, viewer := range map[string]*uuid.UUID{"a signed-in stranger": &stranger, "signed out": nil} {
		denied := r.bySound(sxPrivate, "", viewer)
		wantSoundError(t, denied, http.StatusNotFound, "NOT_FOUND")
		if denied.Body.String() != missing.Body.String() {
			t.Fatalf("%s: denied and missing differ.\n denied  %s\n missing %s", name, denied.Body.String(), missing.Body.String())
		}
		if !reflect.DeepEqual(denied.Header(), missing.Header()) {
			t.Fatalf("%s: headers differ: %v vs %v", name, denied.Header(), missing.Header())
		}
	}
	if len(r.store.listed) != 0 {
		t.Fatalf("the posts of a refused sound were read: %+v", r.store.listed)
	}
	// Its creator may watch the video, so the sound is theirs to open.
	if w := r.bySound(sxPrivate, "", &sxCreator); w.Code != http.StatusOK {
		t.Fatalf("the creator: status=%d body=%s", w.Code, w.Body.String())
	}

	// Not ready, and private to its creator, are the same answer too.
	r.store.tracks[sxSound].Status = "processing"
	if w := r.bySound(sxSound, "", &stranger); w.Body.String() != missing.Body.String() {
		t.Fatalf("a sound still processing: %s", w.Body.String())
	}
	r.store.tracks[sxSound].Status, r.store.tracks[sxSound].IsPublic = "ready", false
	if w := r.bySound(sxSound, "", &stranger); w.Body.String() != missing.Body.String() {
		t.Fatalf("a private sound: %s", w.Body.String())
	}
	if w := r.bySound(sxSound, "", &sxCreator); w.Code != http.StatusOK {
		t.Fatalf("a private sound, for its creator: status=%d", w.Code)
	}
}

// An audience that cannot be decided is a fault, never a listing.
func TestBySoundUnresolvedIsUnavailable(t *testing.T) {
	r := newSoundRig(t)
	r.audienceErr = errors.New("media access store unavailable: dial tcp 10.0.0.5:5432")
	w := r.bySound(sxSound, "", nil)
	wantSoundError(t, w, http.StatusServiceUnavailable, "SOUND_UNAVAILABLE")
	if strings.Contains(w.Body.String(), "10.0.0.5") || strings.Contains(w.Body.String(), "media access") {
		t.Fatalf("the fault's text reached the wire: %s", w.Body.String())
	}
	if len(r.store.listed) != 0 {
		t.Fatalf("the posts were read before the sound was decided: %+v", r.store.listed)
	}
}

// The origin is on the first page only and is never a row, and the cursor
// walks the rest.
func TestBySoundOriginAndPaging(t *testing.T) {
	r := newSoundRig(t)
	// A source reel that (oddly) plays its own sound would otherwise be a row.
	r.store.posts[sxOrigin].AudioTrackID = &sxSound

	first, next := decodeBySound(t, r.bySound(sxSound, "?limit=1", &fxViewer))
	if idOf(t, first.Origin) != sxOrigin {
		t.Fatalf("first page origin = %v", first.Origin)
	}
	if len(first.Items) != 1 || idOf(t, first.Items[0]) != sxReelA || next == "" {
		t.Fatalf("first page items=%v next=%q", first.Items, next)
	}
	if !service.ValidSoundPostsCursor(next) {
		t.Fatalf("next_cursor %q is not one the route accepts", next)
	}

	w := r.bySound(sxSound, "?limit=1&cursor="+next, &fxViewer)
	second, last := decodeBySound(t, w)
	var top map[string]json.RawMessage
	_ = json.Unmarshal(decodeSound(t, w).Data, &top)
	if string(top["origin"]) != "null" {
		t.Fatalf("the origin was repeated on the second page: %s", top["origin"])
	}
	if len(second.Items) != 1 || idOf(t, second.Items[0]) != sxReelB || last != "" {
		t.Fatalf("second page items=%v next=%q", second.Items, last)
	}
	if second.Sound == nil {
		t.Fatal("the second page lost the sound")
	}
	for _, page := range []bySoundData{first, second} {
		for _, item := range page.Items {
			if idOf(t, item) == sxOrigin {
				t.Fatal("the origin is repeated in items")
			}
		}
	}
	for _, call := range r.store.listed {
		if call.exclude == nil || *call.exclude != sxOrigin || call.limit != 1 {
			t.Fatalf("the store was asked %+v, want the origin excluded and limit 1", call)
		}
	}
}

// An origin this viewer may not open is null; the page is still answered.
func TestBySoundOriginNeedsTheReadGate(t *testing.T) {
	r := newSoundRig(t)
	r.unreadable[sxOrigin] = true
	w := r.bySound(sxSound, "", &fxViewer)
	data, _ := decodeBySound(t, w)
	var top map[string]json.RawMessage
	_ = json.Unmarshal(decodeSound(t, w).Data, &top)
	if string(top["origin"]) != "null" || len(data.Items) != 2 {
		t.Fatalf("origin=%s items=%d", top["origin"], len(data.Items))
	}

	// A sound that names no source has no origin either, and an empty list
	// is an array.
	g := newSoundRig(t)
	g.store.tracks[sxSound].SourcePostID = nil
	delete(g.store.posts, sxReelA)
	delete(g.store.posts, sxReelB)
	w = g.bySound(sxSound, "", nil)
	_ = json.Unmarshal(decodeSound(t, w).Data, &top)
	if w.Code != http.StatusOK || string(top["origin"]) != "null" || string(top["items"]) != "[]" {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

// The author gate of every public listing: a hidden author's reel is no row.
func TestBySoundDropsHiddenAuthors(t *testing.T) {
	r := newSoundRig(t)
	r.hidden.hidden[fxAuthor] = true // reel A's author
	data, _ := decodeBySound(t, r.bySound(sxSound, "", &fxViewer))
	if len(data.Items) != 1 || idOf(t, data.Items[0]) != sxReelB {
		t.Fatalf("items = %v, want reel B alone", data.Items)
	}
	// Their own reel is still theirs to see.
	mine, _ := decodeBySound(t, r.bySound(sxSound, "", &fxAuthor))
	if len(mine.Items) != 2 {
		t.Fatalf("the author lost their own reel: %v", mine.Items)
	}
}

func TestBySoundRejectsABadRequest(t *testing.T) {
	r := newSoundRig(t)
	wantSoundError(t, r.do(http.MethodGet, "/v1/posts/by-sound/not-a-uuid", nil), http.StatusBadRequest, "INVALID_ID")
	for _, cursor := range []string{"x", "2026-09-29T10:00:00Z", "2026-09-29T10:00:00Z_nope"} {
		wantSoundError(t, r.bySound(sxSound, "?cursor="+cursor, nil), http.StatusBadRequest, "INVALID_CURSOR")
	}
	if len(r.store.listed) != 0 || len(r.asked) != 0 {
		t.Fatalf("a refused request reached the store: %+v %v", r.store.listed, r.asked)
	}
}

func TestBySoundLimit(t *testing.T) {
	for query, want := range map[string]int{"": 24, "?limit=10": 10, "?limit=50": 50, "?limit=500": 50, "?limit=0": 24, "?limit=-4": 24, "?limit=abc": 24} {
		r := newSoundRig(t)
		if w := r.bySound(sxSound, query, nil); w.Code != http.StatusOK {
			t.Fatalf("%q: status=%d body=%s", query, w.Code, w.Body.String())
		}
		if len(r.store.listed) != 1 || r.store.listed[0].limit != want {
			t.Fatalf("%q: the store was asked %+v, want limit %d", query, r.store.listed, want)
		}
	}
}

// ---- POST /v1/posts/:postId/sound ----

func TestUseSoundNeedsAViewerAndAPost(t *testing.T) {
	r := newSoundRig(t)
	wantSoundError(t, r.use(sxOrigin, nil), http.StatusUnauthorized, "UNAUTHORIZED")
	wantSoundError(t, r.do(http.MethodPost, "/v1/posts/not-a-uuid/sound", &fxViewer), http.StatusBadRequest, "INVALID_ID")
	if r.media.calls != 0 {
		t.Fatalf("media-service was asked %d times", r.media.calls)
	}
}

// A post the viewer may not open is the 404 GET /v1/posts/:postId gives,
// whether it exists or not.
func TestUseSoundUnreadablePostIsNotFound(t *testing.T) {
	r := newSoundRig(t)
	r.unreadable[sxOrigin] = true
	hidden := r.use(sxOrigin, &fxViewer)
	wantSoundError(t, hidden, http.StatusNotFound, "NOT_FOUND")
	missing := r.use(uuid.New(), &fxViewer)
	wantSoundError(t, missing, http.StatusNotFound, "NOT_FOUND")
	if hidden.Body.String() != missing.Body.String() {
		t.Fatalf("hidden and missing differ.\n hidden  %s\n missing %s", hidden.Body.String(), missing.Body.String())
	}
	if r.media.calls != 0 {
		t.Fatalf("media-service was asked %d times for a post nobody may open", r.media.calls)
	}
}

func TestUseSoundNotAReel(t *testing.T) {
	for name, change := range map[string]func(p *postgres.Post){
		"a long video": func(p *postgres.Post) { p.ContentType = "long_video" },
		"a text post":  func(p *postgres.Post) { p.ContentType, p.Media = "post", nil },
	} {
		r := newSoundRig(t)
		change(r.store.posts[sxOrigin])
		wantSoundError(t, r.use(sxOrigin, &fxViewer), http.StatusUnprocessableEntity, "NOT_A_REEL")
		if r.media.calls != 0 {
			t.Fatalf("%s: media-service was asked", name)
		}
	}

	long := newSoundRig(t)
	m := long.states[sxOriginMedia]
	m.DurationMs = 301000
	long.states[sxOriginMedia] = m
	wantSoundError(t, long.use(sxOrigin, &fxViewer), http.StatusUnprocessableEntity, "TOO_LONG")

	early := newSoundRig(t)
	m = early.states[sxOriginMedia]
	m.ProcessingStatus, m.ModerationStatus = "processing", "pending"
	early.states[sxOriginMedia] = m
	// Still processing, the reel is its author's alone.
	wantSoundError(t, early.use(sxOrigin, &fxViewer), http.StatusNotFound, "NOT_FOUND")
	wantSoundError(t, early.use(sxOrigin, &sxCreator), http.StatusUnprocessableEntity, "NOT_READY")
}

// Creator consent: reuse turned off is a 403 for everyone but the author.
func TestUseSoundReuseTurnedOff(t *testing.T) {
	r := newSoundRig(t)
	r.store.posts[sxOrigin].RemixSetting = "disallow"
	wantSoundError(t, r.use(sxOrigin, &fxViewer), http.StatusForbidden, "SOUND_REUSE_NOT_ALLOWED")
	if r.media.calls != 0 {
		t.Fatalf("media-service was asked %d times for a refused sound", r.media.calls)
	}
	own := r.use(sxOrigin, &sxCreator)
	if own.Code != http.StatusOK || r.media.calls != 1 {
		t.Fatalf("the author: status=%d calls=%d body=%s", own.Code, r.media.calls, own.Body.String())
	}
	for _, setting := range []string{"allow", "allow_audio_only"} {
		g := newSoundRig(t)
		g.store.posts[sxOrigin].RemixSetting = setting
		if w := g.use(sxOrigin, &fxViewer); w.Code != http.StatusOK {
			t.Fatalf("remix_setting %s: status=%d body=%s", setting, w.Code, w.Body.String())
		}
	}
}

// A reel that already plays an added sound answers it; nothing is made.
func TestUseSoundReturnsTheAttachedSound(t *testing.T) {
	r := newSoundRig(t)
	w := r.use(sxReelA, &fxViewer)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var data struct {
		Sound service.PostSound `json:"sound"`
	}
	if err := json.Unmarshal(decodeSound(t, w).Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.Sound.ID != sxSound || data.Sound.StartMs != 0 || data.Sound.UseCount != 3 || data.Sound.Title != "Original sound - Asha" {
		t.Fatalf("sound = %+v", data.Sound)
	}
	if r.media.calls != 0 {
		t.Fatalf("media-service was asked %d times although the reel already plays a sound", r.media.calls)
	}
}

// What post-service sends media-service: the internal key, never a viewer.
func TestUseSoundAsksMediaService(t *testing.T) {
	r := newSoundRig(t)
	w := r.use(sxOrigin, &fxViewer)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	m := r.media
	if m.calls != 1 || m.method != http.MethodPost || m.path != "/v1/media/internal/"+sxOriginMedia.String()+"/sound" {
		t.Fatalf("calls=%d %s %s", m.calls, m.method, m.path)
	}
	if m.headers.Get("X-Internal-Service-Key") != soundTestKey || m.headers.Get("Content-Type") != "application/json" {
		t.Fatalf("headers = %v", m.headers)
	}
	for _, h := range []string{"X-User-Id", "Authorization", "Cookie"} {
		if v := m.headers.Get(h); v != "" {
			t.Fatalf("the request carried %s: %q", h, v)
		}
	}
	var body map[string]any
	if err := json.Unmarshal(m.got, &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"title": "Original sound - Asha", "artist": "Asha",
		"source_post_id": sxOrigin.String(), "creator_user_id": sxCreator.String()}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("body = %s\nwant %v", m.got, want)
	}
}

func TestUseSoundMediaServiceAnswers(t *testing.T) {
	r := newSoundRig(t)
	r.media.status, r.media.body = http.StatusUnprocessableEntity, `{"error":{"code":"TOO_LONG","message":"ffprobe: 301.2 s"}}`
	w := r.use(sxOrigin, &fxViewer)
	wantSoundError(t, w, http.StatusUnprocessableEntity, "TOO_LONG")
	if strings.Contains(w.Body.String(), "ffprobe") {
		t.Fatalf("media-service's text reached the wire: %s", w.Body.String())
	}

	for _, status := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusNotFound} {
		g := newSoundRig(t)
		g.media.status, g.media.body = status, `{"error":{"code":"STORAGE","message":"s3://bucket/key timed out"}}`
		w := g.use(sxOrigin, &fxViewer)
		wantSoundError(t, w, http.StatusServiceUnavailable, "SOUND_UNAVAILABLE")
		if strings.Contains(w.Body.String(), "s3://") || strings.Contains(w.Body.String(), "STORAGE") {
			t.Fatalf("media-service's text reached the wire: %s", w.Body.String())
		}
	}
}

func TestUseSoundMediaServiceDown(t *testing.T) {
	r := newSoundRig(t)
	r.mediaServer.Close()
	w := r.use(sxOrigin, &fxViewer)
	wantSoundError(t, w, http.StatusServiceUnavailable, "SOUND_UNAVAILABLE")
	if strings.Contains(w.Body.String(), "127.0.0.1") || strings.Contains(w.Body.String(), "connect") {
		t.Fatalf("the transport error reached the wire: %s", w.Body.String())
	}
}

// ---- POST /v1/posts: the request carries the start ----

func TestCreateRequestCarriesTheSoundStart(t *testing.T) {
	var req CreatePostRequest
	if err := json.Unmarshal([]byte(`{"text":"x","visibility":"public","audio_track_id":"`+sxSound.String()+`","audio_start_ms":1500}`), &req); err != nil {
		t.Fatal(err)
	}
	if req.AudioTrackID == nil || *req.AudioTrackID != sxSound.String() || req.AudioStartMs == nil || *req.AudioStartMs != 1500 {
		t.Fatalf("audio_track_id=%v audio_start_ms=%v", req.AudioTrackID, req.AudioStartMs)
	}
	// What the handler links: the sound and the start the request named.
	if id, start, ok := requestedSound(&req); !ok || id != sxSound || start != 1500 {
		t.Fatalf("requestedSound = %s %d %v", id, start, ok)
	}
	noStart := CreatePostRequest{AudioTrackID: req.AudioTrackID}
	if id, start, ok := requestedSound(&noStart); !ok || id != sxSound || start != 0 {
		t.Fatalf("no audio_start_ms: %s %d %v", id, start, ok)
	}
	empty, junk := "", "not-a-sound"
	for name, r := range map[string]CreatePostRequest{"no sound": {}, "an empty id": {AudioTrackID: &empty, AudioStartMs: req.AudioStartMs},
		"not a sound id": {AudioTrackID: &junk}} {
		if _, _, ok := requestedSound(&r); ok {
			t.Fatalf("%s was read as a sound", name)
		}
	}
	// A different start is a different request: a retry that moves it is
	// not swallowed as a replay of the first.
	other := req
	start := 3000
	other.AudioStartMs = &start
	a, err := createFingerprint(req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := createFingerprint(other)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("two requests with different audio_start_ms share a fingerprint")
	}
}

// ---- PATCH /v1/reels/drafts/:draftId: the draft sound ----

// A value that is not a sound id is a 400 and never a write: the service
// behind this router has no store, so a write would not return at all.
func TestDraftPatchRefusesAMalformedSound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	New(service.NewForHandlerTests(service.HandlerTestDeps{}), nil).RegisterDraftRoutes(router)
	for _, body := range []string{
		`{"audio_track_id":"not-a-sound"}`,
		`{"caption":"kept","audio_track_id":"1234","audio_start_ms":10}`,
	} {
		req := httptest.NewRequest(http.MethodPatch, "/v1/reels/drafts/"+uuid.NewString(), strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User-Id", fxViewer.String())
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		wantSoundError(t, w, http.StatusBadRequest, "INVALID_REQUEST")
	}
}

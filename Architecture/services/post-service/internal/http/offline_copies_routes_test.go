package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Offline copies (offline_copies.go) on the REAL router, with in-memory
// stores behind service.NewForHandlerTests: the route inventory, the static
// siblings of /:postId, every status the routes answer, and the golden
// documents the web and Android clients are written against
// (testdata/contracts/mtube/offline_*.json, produced from the routes' own
// responses). The rules themselves are proven in
// internal/service/offline_copies_test.go.

var offlineRoutes = []string{
	"GET /v1/posts/offline",
	"POST /v1/posts/offline/check",
	"POST /v1/posts/:postId/offline",
	"DELETE /v1/posts/:postId/offline",
}

const offlineFxDevice = "web-7f3a9c2e51d84b06"

// ── in-memory storage ──────────────────────────────────────────────────────

type offlineRouteKey struct {
	user, post uuid.UUID
	device     string
}

type offlineRouteStore struct {
	posts map[uuid.UUID]*postgres.Post
	rows  map[offlineRouteKey]*postgres.OfflineCopy
	limit bool // answer every new grant with the limit
}

func (f *offlineRouteStore) GetPost(_ context.Context, id uuid.UUID) (*postgres.Post, error) {
	p := f.posts[id]
	if p == nil || p.DeletedAt != nil {
		return nil, nil
	}
	cp := *p
	cp.Media = append([]postgres.PostMedia(nil), p.Media...)
	return &cp, nil
}

func (f *offlineRouteStore) GetPostsByIDs(ctx context.Context, ids []uuid.UUID) ([]postgres.Post, error) {
	var out []postgres.Post
	for _, id := range ids {
		if p, _ := f.GetPost(ctx, id); p != nil {
			out = append(out, *p)
		}
	}
	return out, nil
}

func (f *offlineRouteStore) GrantOfflineCopy(_ context.Context, userID, postID uuid.UUID, deviceID string, now, expiresAt time.Time, _ int, card json.RawMessage) (*postgres.OfflineCopy, bool, error) {
	key := offlineRouteKey{userID, postID, deviceID}
	existing := f.rows[key]
	wasActive := existing != nil && existing.Active(now)
	if !wasActive && f.limit {
		return nil, false, postgres.ErrOfflineCopyLimit
	}
	row := &postgres.OfflineCopy{UserID: userID, PostID: postID, DeviceID: deviceID, GrantedAt: now, ExpiresAt: expiresAt, Card: card}
	if wasActive {
		row.GrantedAt = existing.GrantedAt
	}
	f.rows[key] = row
	cp := *row
	return &cp, !wasActive, nil
}

func (f *offlineRouteStore) OfflineCopiesForPosts(_ context.Context, userID uuid.UUID, deviceID string, postIDs []uuid.UUID) (map[uuid.UUID]postgres.OfflineCopy, error) {
	out := map[uuid.UUID]postgres.OfflineCopy{}
	for _, id := range postIDs {
		if r := f.rows[offlineRouteKey{userID, id, deviceID}]; r != nil {
			out[id] = *r
		}
	}
	return out, nil
}

func (f *offlineRouteStore) ListActiveOfflineCopies(_ context.Context, userID uuid.UUID, deviceID string, now time.Time) ([]postgres.OfflineCopy, error) {
	out := []postgres.OfflineCopy{}
	for k, r := range f.rows {
		if k.user == userID && k.device == deviceID && r.Active(now) {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].GrantedAt.Equal(out[j].GrantedAt) {
			return out[i].GrantedAt.After(out[j].GrantedAt)
		}
		return out[i].PostID.String() < out[j].PostID.String()
	})
	return out, nil
}

func (f *offlineRouteStore) TouchOfflineCopies(_ context.Context, userID uuid.UUID, deviceID string, postIDs []uuid.UUID, now time.Time) error {
	for _, id := range postIDs {
		if r := f.rows[offlineRouteKey{userID, id, deviceID}]; r != nil {
			at := now
			r.LastCheckedAt = &at
		}
	}
	return nil
}

func (f *offlineRouteStore) RevokeOfflineCopies(_ context.Context, userID uuid.UUID, deviceID string, postIDs []uuid.UUID, reason string, now time.Time) (int64, error) {
	var n int64
	for _, id := range postIDs {
		if r := f.rows[offlineRouteKey{userID, id, deviceID}]; r != nil && r.RevokedAt == nil {
			at := now
			r.RevokedAt, r.RevokeReason = &at, reason
			n++
		}
	}
	return n, nil
}

type offlineRouteGraph struct {
	rels map[string]service.ViewerRelationship
	err  error
}

func (g *offlineRouteGraph) Following(context.Context, string) ([]string, error) { return nil, nil }
func (g *offlineRouteGraph) RelationshipBatch(_ context.Context, _ string, targets []string) (map[string]service.ViewerRelationship, error) {
	if g.err != nil {
		return nil, g.err
	}
	out := make(map[string]service.ViewerRelationship, len(targets))
	for _, id := range targets {
		out[id] = g.rels[id]
	}
	return out, nil
}

// offlineRouteCaptions answers one published English track for every video,
// at the path media-service serves it as WebVTT.
type offlineRouteCaptions struct{}

func (offlineRouteCaptions) CaptionTracks(_ context.Context, _ uuid.UUID, mediaID uuid.UUID) ([]service.OfflineCaption, error) {
	return []service.OfflineCaption{{Lang: "en", Label: "English", Path: "/v1/subtitles/" + mediaID.String() + "/track/en.vtt"}}, nil
}

// offlineRouteChannels is esChannels with the batch read the card's
// channel_name comes from.
type offlineRouteChannels struct{ *esChannels }

func (f offlineRouteChannels) GetChannelsByUserIDs(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]*postgres.Channel, error) {
	out := map[uuid.UUID]*postgres.Channel{}
	for _, id := range ids {
		if ch, ok := f.byUser[id]; ok {
			cp := *ch
			out[id] = &cp
		}
	}
	return out, nil
}

// ── the rig ────────────────────────────────────────────────────────────────

type offlineRouteRig struct {
	router *gin.Engine
	store  *offlineRouteStore
	graph  *offlineRouteGraph
	media  *feedRouteMedia
	states soundRouteStates
	now    time.Time
	// mayHear is the audience decision on the reel's added sound.
	mayHear bool
}

// newOfflineRouteRig serves the fixture long video (fixturePost: public,
// approved, published, allow_download, with a cover) by fxAuthor's channel,
// with a ready 720p / 480p ladder and one English caption track; and a reel
// (sxReelA) that plays an added sound over its own muted audio.
func newOfflineRouteRig(t *testing.T) *offlineRouteRig {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := &offlineRouteRig{
		store:   &offlineRouteStore{posts: map[uuid.UUID]*postgres.Post{}, rows: map[offlineRouteKey]*postgres.OfflineCopy{}},
		graph:   &offlineRouteGraph{rels: map[string]service.ViewerRelationship{}},
		now:     fxTime,
		mayHear: true,
	}
	r.store.posts[fxPost] = fixturePost()
	r.states = soundRouteStates{fxMedia: {UploaderID: fxAuthor, Kind: "video", ProcessingStatus: "ready", ModerationStatus: "passed", DurationMs: 725000, HasHLS: true}}
	size720, size480 := int64(184320000), int64(92160000)
	r.media = &feedRouteMedia{records: map[uuid.UUID]*service.FeedMediaRecord{
		fxMedia: {FileType: "video", MimeType: "video/quicktime", FileSizeBytes: 500000000, ProcessingStatus: "ready", ModerationStatus: "passed",
			Variants: []service.FeedMediaVariant{
				{Name: "480p", Mime: "video/mp4", SizeBytes: &size480, ObjectKey: "user/x/480p"},
				{Name: "720p", Mime: "video/mp4", SizeBytes: &size720, ObjectKey: "user/x/720p"},
			}},
	}}
	// The reel: no cover of its own (the poster is the video's thumbnail),
	// a 480p rendition only, and an added sound from 1.5 s with the reel's
	// own audio muted.
	startMs := 1500
	published := fxTime.Add(-time.Hour)
	r.store.posts[sxReelA] = &postgres.Post{
		ID: sxReelA, AuthorID: fxAuthor, Text: "Monsoon walk, my take", Visibility: "public", ContentType: "flick",
		ReviewStatus: "approved", AllowDownload: true, ContentTypeExplicit: true, CreatedAt: published, UpdatedAt: published,
		PublishedAt: &published, AudioTrackID: &sxSound, AudioStartMs: &startMs, OriginalAudioVol: 0, OverlayAudioVol: 1,
		Media: []postgres.PostMedia{{MediaID: sxReelAMedia, Kind: "video", Position: 0}},
	}
	r.states[sxReelAMedia] = postgres.MediaOwnership{UploaderID: fxAuthor, Kind: "video", ProcessingStatus: "ready", ModerationStatus: "passed", DurationMs: 28400, HasHLS: true}
	reel480, thumb := int64(6144000), int64(20480)
	r.media.records[sxReelAMedia] = &service.FeedMediaRecord{FileType: "video", MimeType: "video/mp4", FileSizeBytes: 30000000,
		ProcessingStatus: "ready", ModerationStatus: "passed", Variants: []service.FeedMediaVariant{
			{Name: "thumb_300", Mime: "image/jpeg", SizeBytes: &thumb, ObjectKey: "user/x/reel/thumb_300"},
			{Name: "480p", Mime: "video/mp4", SizeBytes: &reel480, ObjectKey: "user/x/reel/480p"},
		}}
	sounds := &soundRouteStore{posts: map[uuid.UUID]*postgres.Post{}, sources: map[uuid.UUID][]postgres.SoundSourcePost{},
		tracks: map[uuid.UUID]*postgres.AudioTrack{sxSound: {ID: sxSound, Title: "Original sound - Asha", Artist: "Asha", DurationMs: 28400,
			MediaID: &sxOriginMedia, Status: "ready", IsPublic: true}}}
	chans := offlineRouteChannels{&esChannels{subs: map[[2]uuid.UUID]bool{},
		byUser: map[uuid.UUID]*postgres.Channel{fxAuthor: {UserID: fxAuthor, Name: "Raghu Builds", Handle: "raghu.builds"}}}}
	deps := service.HandlerTestDeps{
		Offline: r.store, Relationships: r.graph, FeedMedia: r.media, MediaStates: r.states, Channels: chans,
		SoundReads: sounds,
		SoundAudience: func(_ context.Context, _ uuid.UUID, media []uuid.UUID) (map[uuid.UUID]bool, error) {
			out := map[uuid.UUID]bool{}
			for _, id := range media {
				out[id] = r.mayHear
			}
			return out, nil
		},
		OfflineCaptions:    offlineRouteCaptions{},
		OfflineEntitlement: func(context.Context, uuid.UUID, *postgres.Post) (bool, error) { return false, nil },
		Now:                func() time.Time { return r.now },
	}
	r.router = gin.New()
	New(service.NewForHandlerTests(deps), nil).RegisterRoutes(r.router)
	return r
}

func (r *offlineRouteRig) do(method, path string, viewer uuid.UUID, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if viewer != uuid.Nil {
		req.Header.Set("X-User-Id", viewer.String())
	}
	w := httptest.NewRecorder()
	r.router.ServeHTTP(w, req)
	return w
}

func (r *offlineRouteRig) grant(viewer uuid.UUID) *httptest.ResponseRecorder {
	return r.do(http.MethodPost, "/v1/posts/"+fxPost.String()+"/offline", viewer, `{"device_id":"`+offlineFxDevice+`"}`)
}

func offlineData(t *testing.T, w *httptest.ResponseRecorder) json.RawMessage {
	t.Helper()
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil || len(env.Data) == 0 {
		t.Fatalf("no data envelope: %v body=%s", err, w.Body.String())
	}
	return env.Data
}

// ── inventory ──────────────────────────────────────────────────────────────

func TestOfflineRoutesAreRegisteredAndStatic(t *testing.T) {
	routes := registeredRoutes(t)
	for _, want := range offlineRoutes {
		if !routes[want] {
			t.Errorf("%s is not registered", want)
		}
	}
	// "offline" beside /:postId resolves as the static route: an anonymous
	// call is the offline routes' 401, never GetPost's 400 INVALID_ID for
	// the word "offline" and never the 404 of an unmatched path.
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/v1/posts/offline"},
		{http.MethodGet, "/v1/posts/offline?device_id=x"},
		{http.MethodPost, "/v1/posts/offline/check"},
	} {
		w := performMTubeRequest(t, c.method, c.path, nil, `{}`)
		if w.Code != http.StatusUnauthorized || errorCode(t, w) != "UNAUTHORIZED" {
			t.Errorf("%s %s: status=%d body=%s, want 401 (static segment read as an id?)", c.method, c.path, w.Code, w.Body.String())
		}
	}
}

// Every offline route refuses a caller with no identity before anything
// reaches the service (the Handler here has a nil svc: getting past the
// check would panic).
func TestOfflineRoutesRequireIdentity(t *testing.T) {
	id := uuid.NewString()
	for _, route := range offlineRoutes {
		parts := strings.SplitN(route, " ", 2)
		path := strings.ReplaceAll(parts[1], ":postId", id)
		for name, headers := range map[string]map[string]string{
			"no header":  nil,
			"not a uuid": {"X-User-Id": "someone"},
			"nil uuid":   {"X-User-Id": uuid.Nil.String()},
		} {
			t.Run(route+"/"+name, func(t *testing.T) {
				w := performMTubeRequest(t, parts[0], path, headers, `{"device_id":"d"}`)
				if w.Code != http.StatusUnauthorized || errorCode(t, w) != "UNAUTHORIZED" {
					t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
				}
			})
		}
	}
}

func TestOfflineHandlerValidationBeforeService(t *testing.T) {
	me := map[string]string{"X-User-Id": uuid.NewString()}
	id := uuid.NewString()
	cases := []struct {
		name, method, path, body string
		status                   int
		code                     string
	}{
		{"grant: bad post id", http.MethodPost, "/v1/posts/nope/offline", `{"device_id":"d"}`, 400, "INVALID_ID"},
		{"grant: bad json", http.MethodPost, "/v1/posts/" + id + "/offline", `{"device_id":`, 400, "INVALID_REQUEST"},
		{"grant: no body", http.MethodPost, "/v1/posts/" + id + "/offline", ``, 400, "INVALID_REQUEST"},
		{"check: bad json", http.MethodPost, "/v1/posts/offline/check", `[`, 400, "INVALID_REQUEST"},
		{"check: post_ids not a list", http.MethodPost, "/v1/posts/offline/check", `{"device_id":"d","post_ids":"x"}`, 400, "INVALID_REQUEST"},
		{"remove: bad post id", http.MethodDelete, "/v1/posts/nope/offline?device_id=d", ``, 400, "INVALID_ID"},
		{"remove: bad json", http.MethodDelete, "/v1/posts/" + id + "/offline", `{"device_id":`, 400, "INVALID_REQUEST"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := performMTubeRequest(t, tc.method, tc.path, me, tc.body)
			if w.Code != tc.status || errorCode(t, w) != tc.code {
				t.Fatalf("status=%d code=%s want %d %s body=%s", w.Code, errorCode(t, w), tc.status, tc.code, w.Body.String())
			}
		})
	}
}

// ── the routes end to end ──────────────────────────────────────────────────

func TestOfflineGrantRouteCreatedThenRefreshed(t *testing.T) {
	r := newOfflineRouteRig(t)
	first := r.grant(fxViewer)
	if first.Code != http.StatusCreated {
		t.Fatalf("first grant: status=%d body=%s", first.Code, first.Body.String())
	}
	if cc := first.Header().Get("Cache-Control"); cc != "private, no-store" {
		t.Fatalf("Cache-Control = %q", cc)
	}
	var card service.OfflineCard
	if err := json.Unmarshal(offlineData(t, first), &card); err != nil {
		t.Fatal(err)
	}
	if card.PostID != fxPost || card.ContentType != "long_video" || card.Media.Variant != "720p" || card.Media.Path != "/v1/media/"+fxMedia.String()+"/serve/720p" ||
		card.ChannelName != "Raghu Builds" || card.RecheckAfterSeconds != 172800 || !card.ExpiresAt.Equal(fxTime.Add(30*24*time.Hour)) {
		t.Fatalf("card = %+v", card)
	}
	// Nothing about where the bytes live is in the body.
	for _, leak := range []string{"user/x/720p", "object_key", "storage", "X-Amz", "Signature"} {
		if strings.Contains(first.Body.String(), leak) {
			t.Fatalf("the card leaks %q: %s", leak, first.Body.String())
		}
	}
	r.now = r.now.Add(24 * time.Hour)
	second := r.grant(fxViewer)
	if second.Code != http.StatusOK {
		t.Fatalf("second grant: status=%d body=%s (want 200: a refresh)", second.Code, second.Body.String())
	}
	_ = json.Unmarshal(offlineData(t, second), &card)
	if !card.ExpiresAt.Equal(r.now.Add(30 * 24 * time.Hour)) {
		t.Fatalf("refresh did not move the expiry: %v", card.ExpiresAt)
	}
}

// Renewing over the wire (2026-10-02): the client repeats the grant with the
// same device_id. 200 and thirty days from now while the copy is active; 403
// once the creator has turned saving off, with the copy left revoked; 201
// when a revoked copy is granted again after the creator turns it back on.
func TestOfflineRenewRoute(t *testing.T) {
	r := newOfflineRouteRig(t)
	if w := r.grant(fxViewer); w.Code != http.StatusCreated {
		t.Fatalf("grant: %d %s", w.Code, w.Body.String())
	}
	checkBody := `{"device_id":"` + offlineFxDevice + `","post_ids":["` + fxPost.String() + `"]}`
	check := func() map[string]any {
		t.Helper()
		w := r.do(http.MethodPost, "/v1/posts/offline/check", fxViewer, checkBody)
		if w.Code != http.StatusOK {
			t.Fatalf("check: %d %s", w.Code, w.Body.String())
		}
		var items []map[string]any
		if err := json.Unmarshal(offlineData(t, w), &items); err != nil || len(items) != 1 {
			t.Fatalf("check body: %v %s", err, w.Body.String())
		}
		return items[0]
	}
	if item := check(); item["valid"] != true || item["renewable"] != true {
		t.Fatalf("check before renewing = %v", item)
	}

	r.now = r.now.Add(20 * 24 * time.Hour)
	renewed := r.grant(fxViewer)
	if renewed.Code != http.StatusOK {
		t.Fatalf("renewal: %d %s (want 200)", renewed.Code, renewed.Body.String())
	}
	var card service.OfflineCard
	if err := json.Unmarshal(offlineData(t, renewed), &card); err != nil {
		t.Fatal(err)
	}
	if !card.ExpiresAt.Equal(r.now.Add(720 * time.Hour)) {
		t.Fatalf("renewed expires_at = %v, want thirty days from %v", card.ExpiresAt, r.now)
	}

	// The creator turns saving off; the check revokes the copy.
	r.store.posts[fxPost].AllowDownload = false
	if item := check(); item["valid"] != false || item["reason"] != "not_allowed" {
		t.Fatalf("check after downloads were turned off = %v", item)
	} else if _, said := item["renewable"]; said {
		t.Fatalf("an invalid item says renewable: %v", item)
	}
	refused := r.grant(fxViewer)
	if refused.Code != http.StatusForbidden || !strings.Contains(refused.Body.String(), "OFFLINE_NOT_ALLOWED") {
		t.Fatalf("renewal of a copy the post no longer allows: %d %s", refused.Code, refused.Body.String())
	}
	if row := r.store.rows[offlineRouteKey{fxViewer, fxPost, offlineFxDevice}]; row == nil || row.RevokedAt == nil || row.RevokeReason != "not_allowed" {
		t.Fatalf("the refused renewal left the row %+v, want it still revoked", row)
	}

	// Switched back on: the revoked copy can be granted again, as a new one.
	r.store.posts[fxPost].AllowDownload = true
	again := r.grant(fxViewer)
	if again.Code != http.StatusCreated {
		t.Fatalf("grant of a revoked copy: %d %s (want 201)", again.Code, again.Body.String())
	}
	if item := check(); item["valid"] != true || item["renewable"] != true {
		t.Fatalf("check after the new grant = %v", item)
	}
}

// A reel whose added sound this viewer may not hear is saved without it:
// sound is null, never an object with a path that would be refused.
func TestOfflineGrantRouteReelSoundFollowsItsAudience(t *testing.T) {
	r := newOfflineRouteRig(t)
	grant := func() map[string]json.RawMessage {
		t.Helper()
		w := r.do(http.MethodPost, "/v1/posts/"+sxReelA.String()+"/offline", fxViewer, `{"device_id":"`+offlineFxDevice+`"}`)
		if w.Code != http.StatusCreated && w.Code != http.StatusOK {
			t.Fatalf("reel grant: %d %s", w.Code, w.Body.String())
		}
		var card map[string]json.RawMessage
		if err := json.Unmarshal(offlineData(t, w), &card); err != nil {
			t.Fatal(err)
		}
		return card
	}
	if card := grant(); !strings.Contains(string(card["sound"]), `"original_volume":0`) || !strings.Contains(string(card["sound"]), `"overlay_volume":1`) ||
		strings.Contains(string(card["sound"]), "size_bytes") {
		t.Fatalf("sound = %s", card["sound"])
	}
	r.mayHear = false
	if card := grant(); string(card["sound"]) != "null" {
		t.Fatalf("sound = %s, want null", card["sound"])
	}
}

func TestOfflineGrantRouteStatuses(t *testing.T) {
	tier := uuid.New()
	future := fxTime.Add(48 * time.Hour)
	cases := []struct {
		name   string
		setup  func(r *offlineRouteRig)
		viewer uuid.UUID
		body   string
		status int
		code   string
	}{
		{name: "no such post", setup: func(r *offlineRouteRig) { delete(r.store.posts, fxPost) }, status: 404, code: "NOT_FOUND"},
		{name: "private post", setup: func(r *offlineRouteRig) { r.store.posts[fxPost].Visibility = "private" }, status: 404, code: "NOT_FOUND"},
		{name: "blocked by the author", setup: func(r *offlineRouteRig) {
			r.graph.rels[fxAuthor.String()] = service.ViewerRelationship{BlockedBy: true}
		}, status: 404, code: "NOT_FOUND"},
		{name: "blocked the author", setup: func(r *offlineRouteRig) {
			r.graph.rels[fxAuthor.String()] = service.ViewerRelationship{Blocked: true}
		}, status: 404, code: "NOT_FOUND"},
		{name: "someone else's scheduled post", setup: func(r *offlineRouteRig) { r.store.posts[fxPost].PublishAt = &future }, status: 404, code: "NOT_FOUND"},
		{name: "downloads off", setup: func(r *offlineRouteRig) { r.store.posts[fxPost].AllowDownload = false }, status: 403, code: "OFFLINE_NOT_ALLOWED"},
		{name: "members-only, not a member", setup: func(r *offlineRouteRig) { r.store.posts[fxPost].TierRequiredID = &tier }, status: 403, code: "OFFLINE_NOT_ALLOWED"},
		{name: "not a video", setup: func(r *offlineRouteRig) { r.store.posts[fxPost].ContentType = "post" }, status: 422, code: "UNSUPPORTED_CONTENT"},
		{name: "no device id", body: `{}`, status: 422, code: "INVALID_DEVICE"},
		{name: "device id over 64", body: `{"device_id":"` + strings.Repeat("d", 65) + `"}`, status: 422, code: "INVALID_DEVICE"},
		{name: "owner's scheduled post", setup: func(r *offlineRouteRig) { r.store.posts[fxPost].PublishAt = &future }, viewer: fxAuthor, status: 409, code: "NOT_READY"},
		{name: "owner's processing post", setup: func(r *offlineRouteRig) {
			r.states[fxMedia] = postgres.MediaOwnership{UploaderID: fxAuthor, Kind: "video", ProcessingStatus: "processing", ModerationStatus: "pending"}
		}, viewer: fxAuthor, status: 409, code: "NOT_READY"},
		{name: "no rendition", setup: func(r *offlineRouteRig) { r.media.records[fxMedia].Variants = nil }, status: 409, code: "NOT_READY"},
		{name: "100 copies already", setup: func(r *offlineRouteRig) { r.store.limit = true }, status: 409, code: "OFFLINE_LIMIT"},
		{name: "media-service down", setup: func(r *offlineRouteRig) { delete(r.media.records, fxMedia) }, status: 503, code: "DEPENDENCY_UNAVAILABLE"},
		{name: "graph down", setup: func(r *offlineRouteRig) { r.graph.err = errors.New("down") }, status: 503, code: "DEPENDENCY_UNAVAILABLE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newOfflineRouteRig(t)
			if tc.setup != nil {
				tc.setup(r)
			}
			viewer := tc.viewer
			if viewer == uuid.Nil {
				viewer = fxViewer
			}
			body := tc.body
			if body == "" {
				body = `{"device_id":"` + offlineFxDevice + `"}`
			}
			w := r.do(http.MethodPost, "/v1/posts/"+fxPost.String()+"/offline", viewer, body)
			if w.Code != tc.status || errorCode(t, w) != tc.code {
				t.Fatalf("status=%d code=%s want %d %s body=%s", w.Code, errorCode(t, w), tc.status, tc.code, w.Body.String())
			}
			if len(r.store.rows) != 0 {
				t.Fatalf("a refused grant wrote a row")
			}
			// A refusal never says where the bytes are.
			if strings.Contains(w.Body.String(), "/serve/") {
				t.Fatalf("a refusal carries a media path: %s", w.Body.String())
			}
		})
	}
}

// A 404 for a post the caller may not watch is byte-for-byte the 404 of a
// post that does not exist (request id aside): the route cannot be used to
// learn that a private post, or a block, is there.
func TestOfflineGrantRouteHiddenPostIsIndistinguishableFromMissing(t *testing.T) {
	r := newOfflineRouteRig(t)
	missing := r.do(http.MethodPost, "/v1/posts/"+uuid.NewString()+"/offline", fxViewer, `{"device_id":"d"}`)
	r.store.posts[fxPost].Visibility = "private"
	hidden := r.grant(fxViewer)
	r.store.posts[fxPost].Visibility = "public"
	r.graph.rels[fxAuthor.String()] = service.ViewerRelationship{BlockedBy: true}
	blocked := r.grant(fxViewer)
	if missing.Code != 404 || hidden.Code != 404 || blocked.Code != 404 ||
		missing.Body.String() != hidden.Body.String() || missing.Body.String() != blocked.Body.String() {
		t.Fatalf("missing=%d %s\nhidden=%d %s\nblocked=%d %s", missing.Code, missing.Body.String(), hidden.Code, hidden.Body.String(), blocked.Code, blocked.Body.String())
	}
}

func TestOfflineCheckListAndRemoveRoutes(t *testing.T) {
	r := newOfflineRouteRig(t)
	if w := r.grant(fxViewer); w.Code != http.StatusCreated {
		t.Fatalf("grant: %d %s", w.Code, w.Body.String())
	}
	check := func(viewer uuid.UUID, ids ...string) []service.OfflineCheckItem {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"device_id": offlineFxDevice, "post_ids": ids})
		w := r.do(http.MethodPost, "/v1/posts/offline/check", viewer, string(body))
		if w.Code != http.StatusOK {
			t.Fatalf("check: %d %s", w.Code, w.Body.String())
		}
		var items []service.OfflineCheckItem
		if err := json.Unmarshal(offlineData(t, w), &items); err != nil {
			t.Fatal(err)
		}
		return items
	}
	stranger := uuid.NewString()
	items := check(fxViewer, fxPost.String(), stranger)
	if len(items) != 2 || !items[0].Valid || items[0].ExpiresAt == nil || items[1].Valid || items[1].Reason != "unknown" {
		t.Fatalf("items = %+v", items)
	}

	list := r.do(http.MethodGet, "/v1/posts/offline?device_id="+offlineFxDevice, fxViewer, "")
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), "/serve/720p") || !strings.Contains(list.Body.String(), `"variant":"720p"`) {
		t.Fatalf("list: %d %s", list.Code, list.Body.String())
	}
	// Another viewer's list on the same device id is empty, and is `[]`.
	other := r.do(http.MethodGet, "/v1/posts/offline?device_id="+offlineFxDevice, fxAuthor, "")
	if other.Code != http.StatusOK || string(offlineData(t, other)) != "[]" {
		t.Fatalf("other viewer's list: %d %s", other.Code, other.Body.String())
	}
	if w := r.do(http.MethodGet, "/v1/posts/offline", fxViewer, ""); w.Code != 422 || errorCode(t, w) != "INVALID_DEVICE" {
		t.Fatalf("list without device_id: %d %s", w.Code, w.Body.String())
	}

	// The check is live: the creator turns downloads off.
	r.store.posts[fxPost].AllowDownload = false
	if items := check(fxViewer, fxPost.String()); items[0].Valid || items[0].Reason != "not_allowed" {
		t.Fatalf("after downloads off: %+v", items)
	}
	r.store.posts[fxPost].AllowDownload = true

	// 101 ids is refused; an outage is a 503, never an answer.
	many := make([]string, 101)
	for i := range many {
		many[i] = uuid.NewString()
	}
	body, _ := json.Marshal(map[string]any{"device_id": offlineFxDevice, "post_ids": many})
	if w := r.do(http.MethodPost, "/v1/posts/offline/check", fxViewer, string(body)); w.Code != 422 || errorCode(t, w) != "INVALID_REQUEST" {
		t.Fatalf("101 ids: %d %s", w.Code, w.Body.String())
	}
	if w := r.do(http.MethodPost, "/v1/posts/offline/check", fxViewer, `{"post_ids":[]}`); w.Code != 422 || errorCode(t, w) != "INVALID_DEVICE" {
		t.Fatalf("check without device: %d %s", w.Code, w.Body.String())
	}
	if w := r.grant(fxViewer); w.Code != http.StatusCreated { // revoked above by the live check; saved again
		t.Fatalf("re-grant: %d %s", w.Code, w.Body.String())
	}
	r.graph.err = errors.New("graph down")
	body, _ = json.Marshal(map[string]any{"device_id": offlineFxDevice, "post_ids": []string{fxPost.String()}})
	if w := r.do(http.MethodPost, "/v1/posts/offline/check", fxViewer, string(body)); w.Code != 503 || errorCode(t, w) != "DEPENDENCY_UNAVAILABLE" {
		t.Fatalf("check during an outage: %d %s", w.Code, w.Body.String())
	}
	r.graph.err = nil

	// Remove: by body, by query, twice; without a device it is refused.
	path := "/v1/posts/" + fxPost.String() + "/offline"
	if w := r.do(http.MethodDelete, path, fxViewer, ""); w.Code != 422 || errorCode(t, w) != "INVALID_DEVICE" {
		t.Fatalf("remove without device: %d %s", w.Code, w.Body.String())
	}
	if w := r.do(http.MethodDelete, path, fxViewer, `{"device_id":"`+offlineFxDevice+`"}`); w.Code != 200 ||
		string(offlineData(t, w)) != fmt.Sprintf(`{"post_id":"%s","removed":true}`, fxPost) {
		t.Fatalf("remove by body: %d %s", w.Code, w.Body.String())
	}
	if w := r.do(http.MethodDelete, path+"?device_id="+offlineFxDevice, fxViewer, ""); w.Code != 200 {
		t.Fatalf("remove by query, again: %d %s", w.Code, w.Body.String())
	}
	row := r.store.rows[offlineRouteKey{fxViewer, fxPost, offlineFxDevice}]
	if row == nil || row.RevokedAt == nil || row.RevokeReason != "removed" {
		t.Fatalf("row after remove = %+v (it must stay, revoked)", row)
	}
	if items := check(fxViewer, fxPost.String()); items[0].Valid || items[0].Reason != "revoked" {
		t.Fatalf("after remove: %+v", items)
	}
	// The body's device wins over the query's.
	if w := r.grant(fxViewer); w.Code != http.StatusCreated {
		t.Fatal(w.Body.String())
	}
	if w := r.do(http.MethodDelete, path+"?device_id="+offlineFxDevice, fxViewer, `{"device_id":"another-device"}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if items := check(fxViewer, fxPost.String()); !items[0].Valid {
		t.Fatalf("the query's device was used although the body named another: %+v", items)
	}
}

func TestWriteOfflineErrorCodes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{service.ErrPostNotFound, 404, "NOT_FOUND"},
		{service.ErrPostNotVisible, 404, "NOT_FOUND"},
		{service.ErrOfflineNotAllowed, 403, "OFFLINE_NOT_ALLOWED"},
		{service.ErrOfflineUnsupported, 422, "UNSUPPORTED_CONTENT"},
		{service.ErrOfflineDevice, 422, "INVALID_DEVICE"},
		{service.ErrOfflineTooMany, 422, "INVALID_REQUEST"},
		{service.ErrOfflineNotReady, 409, "NOT_READY"},
		{service.ErrOfflineLimit, 409, "OFFLINE_LIMIT"},
		{fmt.Errorf("%w: media record: boom", service.ErrOfflineUnavailable), 503, "DEPENDENCY_UNAVAILABLE"},
		{service.ErrStoryPolicyUnresolved, 503, "DEPENDENCY_UNAVAILABLE"},
		{errors.New("pq: relation does not exist"), 500, "INTERNAL_ERROR"},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		writeOfflineError(c, tc.err)
		if w.Code != tc.status || errorCode(t, w) != tc.code {
			t.Errorf("%v: status=%d code=%s want %d %s", tc.err, w.Code, errorCode(t, w), tc.status, tc.code)
		}
		// Neither a dependency's words nor a database's reach the wire.
		if strings.Contains(w.Body.String(), "boom") || strings.Contains(w.Body.String(), "relation") {
			t.Errorf("%v: internal detail on the wire: %s", tc.err, w.Body.String())
		}
	}
}

// ── golden documents ───────────────────────────────────────────────────────

// offlineContracts produces the documents from the routes' own responses:
// the grant of a long video, the grant of a reel that plays an added sound,
// a check that carries one answer of every kind the fixture can produce,
// and the list (the video and the reel).
func offlineContracts(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	r := newOfflineRouteRig(t)
	grant := r.grant(fxViewer)
	if grant.Code != http.StatusCreated {
		t.Fatalf("grant: %d %s", grant.Code, grant.Body.String())
	}
	r.now = r.now.Add(time.Minute)
	reel := r.do(http.MethodPost, "/v1/posts/"+sxReelA.String()+"/offline", fxViewer, `{"device_id":"`+offlineFxDevice+`"}`)
	if reel.Code != http.StatusCreated {
		t.Fatalf("reel grant: %d %s", reel.Code, reel.Body.String())
	}
	list := r.do(http.MethodGet, "/v1/posts/offline?device_id="+offlineFxDevice, fxViewer, "")
	if list.Code != http.StatusOK {
		t.Fatalf("list: %d %s", list.Code, list.Body.String())
	}
	r.now = fxTime

	// A second copy whose post the creator has since closed to downloads,
	// and one that expired.
	closed := *fixturePost()
	closed.ID, closed.AllowDownload = fxRelated, false
	r.store.posts[fxRelated] = &closed
	r.store.rows[offlineRouteKey{fxViewer, fxRelated, offlineFxDevice}] = &postgres.OfflineCopy{
		UserID: fxViewer, PostID: fxRelated, DeviceID: offlineFxDevice, GrantedAt: fxTime.Add(-time.Hour), ExpiresAt: fxTime.Add(29 * 24 * time.Hour)}
	expired := *fixturePost()
	expired.ID = fxStream
	r.store.posts[fxStream] = &expired
	r.store.rows[offlineRouteKey{fxViewer, fxStream, offlineFxDevice}] = &postgres.OfflineCopy{
		UserID: fxViewer, PostID: fxStream, DeviceID: offlineFxDevice, GrantedAt: fxTime.Add(-31 * 24 * time.Hour), ExpiresAt: fxTime.Add(-24 * time.Hour)}
	body, _ := json.Marshal(map[string]any{"device_id": offlineFxDevice,
		"post_ids": []string{fxPost.String(), fxRelated.String(), fxStream.String(), fxPlaylist.String()}})
	check := r.do(http.MethodPost, "/v1/posts/offline/check", fxViewer, string(body))
	if check.Code != http.StatusOK {
		t.Fatalf("check: %d %s", check.Code, check.Body.String())
	}
	return map[string]json.RawMessage{
		"offline_grant.json":      offlineData(t, grant),
		"offline_grant_reel.json": offlineData(t, reel),
		"offline_check.json":      offlineData(t, check),
		"offline_list.json":       offlineData(t, list),
	}
}

func TestOfflineContracts(t *testing.T) {
	dir := filepath.Join("testdata", "contracts", "mtube")
	update := os.Getenv("UPDATE_CONTRACTS") == "1"
	contracts := offlineContracts(t)
	if len(contracts) != 4 {
		t.Fatalf("%d documents", len(contracts))
	}
	for name, raw := range contracts {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := json.Indent(&buf, raw, "", "  "); err != nil {
				t.Fatal(err)
			}
			got := append(buf.Bytes(), '\n')
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
}

// The fixtures are the pinned contract: these are the names the web and
// Android clients read, checked on the documents themselves so a renamed
// JSON tag cannot pass by regenerating the files.
func TestOfflineContractFieldNamesArePinned(t *testing.T) {
	dir := filepath.Join("testdata", "contracts", "mtube")
	keys := func(raw json.RawMessage) []string {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		out := make([]string, 0, len(m))
		for k := range m {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	read := func(name string) json.RawMessage {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return b
	}
	eq := func(what string, got []string, want ...string) {
		t.Helper()
		sort.Strings(want)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s fields = %v, want %v", what, got, want)
		}
	}
	cardFields := []string{"post_id", "content_type", "expires_at", "recheck_after_seconds", "title", "channel_name", "duration_ms", "poster_path", "media", "captions", "sound"}

	var grant map[string]json.RawMessage
	if err := json.Unmarshal(read("offline_grant.json"), &grant); err != nil {
		t.Fatal(err)
	}
	eq("grant", keys(read("offline_grant.json")), cardFields...)
	eq("grant.media", keys(grant["media"]), "media_id", "variant", "path", "mime", "size_bytes")
	var captions []json.RawMessage
	if err := json.Unmarshal(grant["captions"], &captions); err != nil || len(captions) != 1 {
		t.Fatalf("grant.captions: %v %s", err, grant["captions"])
	}
	eq("grant.captions[0]", keys(captions[0]), "lang", "label", "path")
	if string(grant["sound"]) != "null" {
		t.Errorf("grant.sound = %s, want null on a video with no added sound", grant["sound"])
	}

	if string(grant["content_type"]) != `"long_video"` {
		t.Errorf("grant.content_type = %s", grant["content_type"])
	}

	// The reel: the sound is the six-field object less size_bytes (no length
	// is recorded for a sound, and a guess is never sent), and both volumes
	// are on the wire although one of them is 0.
	var reel map[string]json.RawMessage
	if err := json.Unmarshal(read("offline_grant_reel.json"), &reel); err != nil {
		t.Fatal(err)
	}
	eq("reel", keys(read("offline_grant_reel.json")), cardFields...)
	eq("reel.sound", keys(reel["sound"]), "path", "mime", "start_ms", "original_volume", "overlay_volume")
	var sound struct {
		Path           string   `json:"path"`
		Mime           string   `json:"mime"`
		StartMs        int      `json:"start_ms"`
		OriginalVolume *float64 `json:"original_volume"`
		OverlayVolume  *float64 `json:"overlay_volume"`
	}
	if err := json.Unmarshal(reel["sound"], &sound); err != nil {
		t.Fatal(err)
	}
	if sound.Path != "/v1/audio/"+sxSound.String()+"/serve" || sound.Mime != "audio/mp4" || sound.StartMs != 1500 ||
		sound.OriginalVolume == nil || *sound.OriginalVolume != 0 || sound.OverlayVolume == nil || *sound.OverlayVolume != 1 {
		t.Errorf("reel.sound = %s", reel["sound"])
	}
	if string(reel["content_type"]) != `"flick"` || !strings.Contains(string(reel["poster_path"]), "/serve/thumb_300") {
		t.Errorf("reel content_type=%s poster=%s", reel["content_type"], reel["poster_path"])
	}

	var list []map[string]json.RawMessage
	if err := json.Unmarshal(read("offline_list.json"), &list); err != nil || len(list) != 2 {
		t.Fatalf("list: %v", err)
	}
	for i, row := range list {
		var listKeys []string
		for k := range row {
			listKeys = append(listKeys, k)
		}
		sort.Strings(listKeys)
		eq(fmt.Sprintf("list[%d]", i), listKeys, cardFields...)
		eq(fmt.Sprintf("list[%d].media", i), keys(row["media"]), "media_id", "variant", "mime", "size_bytes") // no path
	}
	if string(list[0]["content_type"]) != `"flick"` || string(list[1]["content_type"]) != `"long_video"` || string(list[0]["sound"]) == "null" {
		t.Errorf("list content types / sound: %s %s %s", list[0]["content_type"], list[1]["content_type"], list[0]["sound"])
	}

	var check []map[string]any
	if err := json.Unmarshal(read("offline_check.json"), &check); err != nil || len(check) != 4 {
		t.Fatalf("check: %v", err)
	}
	wantReasons := []string{"", "not_allowed", "expired", "unknown"}
	for i, item := range check {
		valid, _ := item["valid"].(bool)
		reason, _ := item["reason"].(string)
		_, hasExpiry := item["expires_at"]
		contentType, hasType := item["content_type"].(string)
		renewable, hasRenewable := item["renewable"].(bool)
		wantFields := 3
		if valid {
			wantFields = 5 // post_id, valid, expires_at, content_type, renewable
		}
		// renewable is on a valid item only, and it is a boolean there —
		// never absent, so a client can tell "no" from "not said".
		if valid != (i == 0) || reason != wantReasons[i] || hasExpiry != valid || hasType != valid || len(item) != wantFields ||
			hasRenewable != valid || (valid && !renewable) ||
			(valid && contentType != "long_video") {
			t.Errorf("check[%d] = %v", i, item)
		}
	}
}

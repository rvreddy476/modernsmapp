package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Offline copies (offline_copies.go): every rule of the grant, the check,
// the list and the removal, over in-memory storage. The audience decision is
// the REAL one (postMediaDecisions, with the graph's account gate answered
// by the fake `can` endpoint from privacy_gate_test.go), so a rule that
// playback applies cannot be missing here. The SQL is proven on a *_test
// database in store/postgres/offline_copies_integration_test.go and the
// routes in internal/http/offline_copies_routes_test.go.

// ── in-memory storage ──────────────────────────────────────────────────────

type offlineKey struct {
	user, post uuid.UUID
	device     string
}

type fakeOfflineStore struct {
	posts    map[uuid.UUID]*postgres.Post
	rows     map[offlineKey]*postgres.OfflineCopy
	postsErr error
	grantErr error
	touched  []uuid.UUID
}

func newFakeOfflineStore() *fakeOfflineStore {
	return &fakeOfflineStore{posts: map[uuid.UUID]*postgres.Post{}, rows: map[offlineKey]*postgres.OfflineCopy{}}
}

func (f *fakeOfflineStore) GetPost(_ context.Context, id uuid.UUID) (*postgres.Post, error) {
	if f.postsErr != nil {
		return nil, f.postsErr
	}
	p := f.posts[id]
	if p == nil || p.DeletedAt != nil {
		return nil, nil
	}
	cp := *p
	cp.Media = append([]postgres.PostMedia(nil), p.Media...)
	return &cp, nil
}

func (f *fakeOfflineStore) GetPostsByIDs(ctx context.Context, ids []uuid.UUID) ([]postgres.Post, error) {
	if f.postsErr != nil {
		return nil, f.postsErr
	}
	var out []postgres.Post
	for _, id := range ids {
		if p, _ := f.GetPost(ctx, id); p != nil {
			out = append(out, *p)
		}
	}
	return out, nil
}

func (f *fakeOfflineStore) GrantOfflineCopy(_ context.Context, userID, postID uuid.UUID, deviceID string, now, expiresAt time.Time, limit int, card json.RawMessage) (*postgres.OfflineCopy, bool, error) {
	if f.grantErr != nil {
		return nil, false, f.grantErr
	}
	key := offlineKey{userID, postID, deviceID}
	existing := f.rows[key]
	wasActive := existing != nil && existing.Active(now)
	if !wasActive {
		active := 0
		for k, r := range f.rows {
			if k.user == userID && r.Active(now) {
				active++
			}
		}
		if active >= limit {
			return nil, false, postgres.ErrOfflineCopyLimit
		}
	}
	row := &postgres.OfflineCopy{UserID: userID, PostID: postID, DeviceID: deviceID, GrantedAt: now, ExpiresAt: expiresAt, Card: card}
	if wasActive {
		row.GrantedAt = existing.GrantedAt
	}
	checked := now
	row.LastCheckedAt = &checked
	f.rows[key] = row
	cp := *row
	return &cp, !wasActive, nil
}

func (f *fakeOfflineStore) OfflineCopiesForPosts(_ context.Context, userID uuid.UUID, deviceID string, postIDs []uuid.UUID) (map[uuid.UUID]postgres.OfflineCopy, error) {
	out := map[uuid.UUID]postgres.OfflineCopy{}
	for _, id := range postIDs {
		if r := f.rows[offlineKey{userID, id, deviceID}]; r != nil {
			out[id] = *r
		}
	}
	return out, nil
}

func (f *fakeOfflineStore) ListActiveOfflineCopies(_ context.Context, userID uuid.UUID, deviceID string, now time.Time) ([]postgres.OfflineCopy, error) {
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

func (f *fakeOfflineStore) TouchOfflineCopies(_ context.Context, userID uuid.UUID, deviceID string, postIDs []uuid.UUID, now time.Time) error {
	for _, id := range postIDs {
		if r := f.rows[offlineKey{userID, id, deviceID}]; r != nil {
			checked := now
			r.LastCheckedAt = &checked
			f.touched = append(f.touched, id)
		}
	}
	return nil
}

func (f *fakeOfflineStore) RevokeOfflineCopies(_ context.Context, userID uuid.UUID, deviceID string, postIDs []uuid.UUID, reason string, now time.Time) (int64, error) {
	var n int64
	for _, id := range postIDs {
		if r := f.rows[offlineKey{userID, id, deviceID}]; r != nil && r.RevokedAt == nil {
			at := now
			r.RevokedAt, r.RevokeReason = &at, reason
			n++
		}
	}
	return n, nil
}

type fakeOfflineCaptions struct {
	tracks []OfflineCaption
	err    error
	viewer uuid.UUID
}

func (f *fakeOfflineCaptions) CaptionTracks(_ context.Context, viewerID, _ uuid.UUID) ([]OfflineCaption, error) {
	f.viewer = viewerID
	return f.tracks, f.err
}

// ── the rig ────────────────────────────────────────────────────────────────

const offlineDevice = "device-web-0001"

type offlineRig struct {
	svc      *Service
	store    *fakeOfflineStore
	media    *fakeFeedMedia
	states   fakeMediaStates
	captions *fakeOfflineCaptions
	hidden   *fakeHiddenAuthors
	shares   *fakePrivateShares
	rels     map[string]ViewerRelationship
	relErr   *error
	entitled map[uuid.UUID]bool // viewer -> entitled to the author's tier
	entErr   error
	now      time.Time

	author, private, viewer, minor uuid.UUID
	post, video, cover             uuid.UUID
}

func ip64(v int64) *int64 { return &v }

// newOfflineRig builds a service around one public, approved, published long
// video by `author` that allows downloads, with a ready 720p / 480p / 360p
// ladder. `viewer` is an adult stranger to the author.
func newOfflineRig(t *testing.T) *offlineRig {
	t.Helper()
	r := &offlineRig{
		store: newFakeOfflineStore(), media: newFakeFeedMedia(), states: fakeMediaStates{},
		captions: &fakeOfflineCaptions{tracks: []OfflineCaption{{Lang: "en", Label: "English", Path: "/v1/subtitles/x/track/en.vtt"}}},
		hidden:   &fakeHiddenAuthors{hidden: map[uuid.UUID]bool{}},
		shares:   &fakePrivateShares{shared: map[uuid.UUID]map[uuid.UUID]bool{}},
		rels:     map[string]ViewerRelationship{}, entitled: map[uuid.UUID]bool{},
		now:    time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC),
		author: uuid.New(), private: uuid.New(), viewer: uuid.New(), minor: uuid.New(),
		post: uuid.New(), video: uuid.New(), cover: uuid.New(),
	}
	var relErr error
	r.relErr = &relErr
	var calls int32
	srv := fakeGraphCan(t, map[string]bool{r.author.String(): true, r.private.String(): false}, &calls, nil)
	t.Cleanup(srv.Close)

	r.store.posts[r.post] = &postgres.Post{
		ID: r.post, AuthorID: r.author, Visibility: "public", ReviewStatus: "approved", ContentType: "long_video",
		AllowDownload: true, Title: "Friday build", CoverMediaID: &r.cover,
		Media: []postgres.PostMedia{{MediaID: r.video, Kind: "video"}},
	}
	r.states[r.video] = postgres.MediaOwnership{UploaderID: r.author, Kind: "video", ProcessingStatus: "ready", ModerationStatus: "passed", DurationMs: 725000, HasHLS: true}
	r.media.records[r.video] = &FeedMediaRecord{
		FileType: "video", MimeType: "video/quicktime", FileSizeBytes: 900_000_000, ProcessingStatus: "ready", ModerationStatus: "passed",
		Variants: []FeedMediaVariant{
			{Name: "thumb_150", Mime: "image/jpeg", SizeBytes: ip64(9000), ObjectKey: "k/thumb"},
			{Name: "360p", Mime: "video/mp4", SizeBytes: ip64(40_000_000), ObjectKey: "k/360"},
			{Name: "480p", Mime: "video/mp4", SizeBytes: ip64(90_000_000), ObjectKey: "k/480"},
			{Name: "720p", Mime: "video/mp4", SizeBytes: ip64(184_320_000), ObjectKey: "k/720"},
		},
	}
	r.svc = &Service{
		offline:              r.store,
		feedMedia:            r.media,
		mediaStates:          r.states,
		offlineCaptionTracks: r.captions,
		storyAudience:        NewStoryAudience(offlineRelationships{r}),
		graphServiceURL:      srv.URL,
		internalServiceKey:   "test-key",
		httpClient:           http.DefaultClient,
		birthDates:           &fakeBirthDates{dob: map[uuid.UUID]*time.Time{r.viewer: dayp(1990, 1, 1), r.minor: dayp(2015, 1, 1)}},
		privateShare:         r.shares,
		now:                  func() time.Time { return r.now },
	}
	r.svc.offlineEntitlement = func(_ context.Context, viewerID uuid.UUID, _ *postgres.Post) (bool, error) {
		if r.entErr != nil {
			return false, r.entErr
		}
		return r.entitled[viewerID], nil
	}
	r.svc.hiddenAuthors = r.hidden
	return r
}

// offlineRelationships reads the rig's relationship map live, so a test can
// add a block after a grant.
type offlineRelationships struct{ r *offlineRig }

func (o offlineRelationships) Following(context.Context, string) ([]string, error) { return nil, nil }
func (o offlineRelationships) RelationshipBatch(_ context.Context, _ string, targets []string) (map[string]ViewerRelationship, error) {
	if *o.r.relErr != nil {
		return nil, *o.r.relErr
	}
	out := make(map[string]ViewerRelationship, len(targets))
	for _, id := range targets {
		out[id] = o.r.rels[id]
	}
	return out, nil
}

func (r *offlineRig) p() *postgres.Post { return r.store.posts[r.post] }

func (r *offlineRig) grant(viewer uuid.UUID) (*OfflineCard, bool, error) {
	return r.svc.GrantOfflineCopy(context.Background(), viewer, r.post, offlineDevice)
}

func (r *offlineRig) mustGrant(t *testing.T, viewer uuid.UUID) *OfflineCard {
	t.Helper()
	card, _, err := r.grant(viewer)
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	return card
}

func (r *offlineRig) check(t *testing.T, viewer uuid.UUID, ids ...string) []OfflineCheckItem {
	t.Helper()
	items, err := r.svc.CheckOfflineCopies(context.Background(), viewer, offlineDevice, ids)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	return items
}

func (r *offlineRig) row(viewer uuid.UUID) *postgres.OfflineCopy {
	return r.store.rows[offlineKey{viewer, r.post, offlineDevice}]
}

func wantOfflineErr(t *testing.T, err, want error, status int, code string) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
	if s, c := OfflineErrorStatus(err); s != status || c != code {
		t.Fatalf("status = %d %s, want %d %s", s, c, status, code)
	}
}

// ── the grant ──────────────────────────────────────────────────────────────

func TestOfflineGrantHandsOutTheCardAndThirtyDays(t *testing.T) {
	r := newOfflineRig(t)
	card, created, err := r.grant(r.viewer)
	if err != nil || !created {
		t.Fatalf("grant: created=%v err=%v", created, err)
	}
	if card.PostID != r.post || card.ContentType != "long_video" || card.Title != "Friday build" || card.DurationMs != 725000 {
		t.Fatalf("card = %+v", card)
	}
	if !card.ExpiresAt.Equal(r.now.Add(30*24*time.Hour)) || card.RecheckAfterSeconds != 172800 {
		t.Fatalf("expiry = %v recheck = %d", card.ExpiresAt, card.RecheckAfterSeconds)
	}
	want := OfflineMedia{MediaID: r.video, Variant: "720p", Path: "/v1/media/" + r.video.String() + "/serve/720p", Mime: "video/mp4", SizeBytes: 184_320_000}
	if card.Media != want {
		t.Fatalf("media = %+v, want %+v", card.Media, want)
	}
	if card.PosterPath != "/v1/media/"+r.cover.String()+"/serve" {
		t.Fatalf("poster = %q", card.PosterPath)
	}
	if len(card.Captions) != 1 || card.Captions[0].Lang != "en" || card.Sound != nil {
		t.Fatalf("captions = %+v sound = %+v", card.Captions, card.Sound)
	}
	if r.captions.viewer != r.viewer {
		t.Fatalf("captions were asked as %v, want the viewer", r.captions.viewer)
	}
	row := r.row(r.viewer)
	if row == nil || !row.Active(r.now) || strings.Contains(string(row.Card), "k/720") || strings.Contains(string(row.Card), `"path":"/v1/media`) {
		t.Fatalf("stored row = %+v card = %s", row, row.Card)
	}
}

func TestOfflineGrantAgainRefreshesTheSameCopy(t *testing.T) {
	r := newOfflineRig(t)
	first := r.mustGrant(t, r.viewer)
	grantedAt := r.row(r.viewer).GrantedAt
	r.now = r.now.Add(10 * 24 * time.Hour)
	second, created, err := r.grant(r.viewer)
	if err != nil || created {
		t.Fatalf("second grant: created=%v err=%v (want a refresh)", created, err)
	}
	if !second.ExpiresAt.Equal(first.ExpiresAt.Add(10 * 24 * time.Hour)) {
		t.Fatalf("expiry not refreshed: %v then %v", first.ExpiresAt, second.ExpiresAt)
	}
	if got := r.row(r.viewer).GrantedAt; !got.Equal(grantedAt) {
		t.Fatalf("granted_at moved on a refresh: %v -> %v", grantedAt, got)
	}
	if len(r.store.rows) != 1 {
		t.Fatalf("rows = %d, want one per (user, post, device)", len(r.store.rows))
	}
}

func TestOfflineVariantIs720Then480Then360AndNeverTheOriginal(t *testing.T) {
	r := newOfflineRig(t)
	rec := r.media.records[r.video]
	drop := func(name string) {
		var kept []FeedMediaVariant
		for _, v := range rec.Variants {
			if v.Name != name {
				kept = append(kept, v)
			}
		}
		rec.Variants = kept
	}
	for _, want := range []string{"720p", "480p", "360p"} {
		card := r.mustGrant(t, r.viewer)
		if card.Media.Variant != want || !strings.HasSuffix(card.Media.Path, "/serve/"+want) {
			t.Fatalf("variant = %s path = %s, want %s", card.Media.Variant, card.Media.Path, want)
		}
		drop(want)
	}
	// Only the original (and an "original" variant row) is left: nothing to copy.
	rec.Variants = append(rec.Variants, FeedMediaVariant{Name: "original", Mime: "video/quicktime", SizeBytes: ip64(900_000_000), ObjectKey: "k/orig"})
	_, _, err := r.grant(r.viewer)
	wantOfflineErr(t, err, ErrOfflineNotReady, 409, "NOT_READY")

	// A rendition row with no object behind it is not a rendition.
	rec.Variants = []FeedMediaVariant{{Name: "720p", Mime: "video/mp4", SizeBytes: ip64(1)}, {Name: "480p", Mime: "", ObjectKey: "k/480"}}
	card := r.mustGrant(t, r.viewer)
	if card.Media.Variant != "480p" || card.Media.Mime != "video/mp4" || card.Media.SizeBytes != 0 {
		t.Fatalf("media = %+v", card.Media)
	}
	// A rendition with no recorded length carries no size_bytes at all: the
	// client verifies a copy against it, and 0 would fail every copy.
	if b, _ := json.Marshal(card.Media); strings.Contains(string(b), "size_bytes") {
		t.Fatalf("an unknown size is on the wire: %s", b)
	}
	// A zero or negative recorded length is not a length either.
	for _, bad := range []int64{0, -1} {
		rec.Variants = []FeedMediaVariant{{Name: "720p", Mime: "video/mp4", SizeBytes: ip64(bad), ObjectKey: "k/720"}}
		if b, _ := json.Marshal(r.mustGrant(t, r.viewer).Media); strings.Contains(string(b), "size_bytes") {
			t.Fatalf("size %d is on the wire: %s", bad, b)
		}
	}
	// A recorded one is sent exactly.
	rec.Variants = []FeedMediaVariant{{Name: "720p", Mime: "video/mp4", SizeBytes: ip64(184_320_001), ObjectKey: "k/720"}}
	if b, _ := json.Marshal(r.mustGrant(t, r.viewer).Media); !strings.Contains(string(b), `"size_bytes":184320001`) {
		t.Fatalf("recorded size not sent exactly: %s", b)
	}
}

func TestOfflineGrantRefusals(t *testing.T) {
	tier := uuid.New()
	cases := []struct {
		name   string
		mutate func(r *offlineRig)
		viewer func(r *offlineRig) uuid.UUID
		want   error
		status int
		code   string
	}{
		{name: "signed out", viewer: func(*offlineRig) uuid.UUID { return uuid.Nil }, want: ErrPostNotFound, status: 404, code: "NOT_FOUND"},
		{name: "no such post", mutate: func(r *offlineRig) { delete(r.store.posts, r.post) }, want: ErrPostNotFound, status: 404, code: "NOT_FOUND"},
		{name: "deleted post", mutate: func(r *offlineRig) { r.p().DeletedAt = dayp(2026, 10, 1) }, want: ErrPostNotFound, status: 404, code: "NOT_FOUND"},
		{name: "private post", mutate: func(r *offlineRig) { r.p().Visibility = "private" }, want: ErrPostNotFound, status: 404, code: "NOT_FOUND"},
		{name: "followers-only, not a follower", mutate: func(r *offlineRig) { r.p().Visibility = "followers" }, want: ErrPostNotFound, status: 404, code: "NOT_FOUND"},
		{name: "retired circle audience", mutate: func(r *offlineRig) { r.p().Visibility = "circle" }, want: ErrPostNotFound, status: 404, code: "NOT_FOUND"},
		{name: "viewer blocked the author", mutate: func(r *offlineRig) { r.rels[r.author.String()] = ViewerRelationship{Blocked: true} }, want: ErrPostNotFound, status: 404, code: "NOT_FOUND"},
		{name: "author blocked the viewer", mutate: func(r *offlineRig) { r.rels[r.author.String()] = ViewerRelationship{BlockedBy: true} }, want: ErrPostNotFound, status: 404, code: "NOT_FOUND"},
		{name: "muted author", mutate: func(r *offlineRig) { r.rels[r.author.String()] = ViewerRelationship{Muted: true} }, want: ErrPostNotFound, status: 404, code: "NOT_FOUND"},
		{name: "private account", mutate: func(r *offlineRig) {
			r.p().AuthorID = r.private
		}, want: ErrPostNotFound, status: 404, code: "NOT_FOUND"},
		{name: "hidden author", mutate: func(r *offlineRig) { r.hidden.hidden[r.author] = true }, want: ErrPostNotFound, status: 404, code: "NOT_FOUND"},
		{name: "pending review", mutate: func(r *offlineRig) { r.p().ReviewStatus = "pending" }, want: ErrPostNotFound, status: 404, code: "NOT_FOUND"},
		{name: "someone else's scheduled post", mutate: func(r *offlineRig) { r.p().PublishAt = dayp(2030, 1, 1) }, want: ErrPostNotFound, status: 404, code: "NOT_FOUND"},
		{name: "someone else's processing post", mutate: func(r *offlineRig) {
			m := r.states[r.video]
			m.ProcessingStatus = "processing"
			r.states[r.video] = m
		}, want: ErrPostNotFound, status: 404, code: "NOT_FOUND"},
		{name: "age-restricted, viewer under 18", mutate: func(r *offlineRig) { r.p().AgeRestricted = true },
			viewer: func(r *offlineRig) uuid.UUID { return r.minor }, want: ErrPostNotFound, status: 404, code: "NOT_FOUND"},
		{name: "downloads turned off", mutate: func(r *offlineRig) { r.p().AllowDownload = false }, want: ErrOfflineNotAllowed, status: 403, code: "OFFLINE_NOT_ALLOWED"},
		{name: "members-only, not a member", mutate: func(r *offlineRig) { r.p().TierRequiredID = &tier }, want: ErrOfflineNotAllowed, status: 403, code: "OFFLINE_NOT_ALLOWED"},
		{name: "members-only, monetization down", mutate: func(r *offlineRig) { r.p().TierRequiredID = &tier; r.entErr = errors.New("down") },
			want: ErrOfflineUnavailable, status: 503, code: "DEPENDENCY_UNAVAILABLE"},
		{name: "a text post", mutate: func(r *offlineRig) { r.p().ContentType = "post" }, want: ErrOfflineUnsupported, status: 422, code: "UNSUPPORTED_CONTENT"},
		{name: "a voice post", mutate: func(r *offlineRig) { r.p().ContentType = "voice" }, want: ErrOfflineUnsupported, status: 422, code: "UNSUPPORTED_CONTENT"},
		{name: "a video post with no video", mutate: func(r *offlineRig) { r.p().Media = nil }, want: ErrOfflineUnsupported, status: 422, code: "UNSUPPORTED_CONTENT"},
		{name: "owner's scheduled post", mutate: func(r *offlineRig) { r.p().PublishAt = dayp(2030, 1, 1) },
			viewer: func(r *offlineRig) uuid.UUID { return r.author }, want: ErrOfflineNotReady, status: 409, code: "NOT_READY"},
		{name: "owner's processing post", mutate: func(r *offlineRig) {
			m := r.states[r.video]
			m.ProcessingStatus = "processing"
			r.states[r.video] = m
		}, viewer: func(r *offlineRig) uuid.UUID { return r.author }, want: ErrOfflineNotReady, status: 409, code: "NOT_READY"},
		{name: "no rendition yet", mutate: func(r *offlineRig) { r.media.records[r.video].Variants = nil }, want: ErrOfflineNotReady, status: 409, code: "NOT_READY"},
		{name: "media record still processing", mutate: func(r *offlineRig) { r.media.records[r.video].ProcessingStatus = "processing" },
			want: ErrOfflineNotReady, status: 409, code: "NOT_READY"},
		{name: "media-service down", mutate: func(r *offlineRig) { r.media.errs[r.video] = errFeedMediaUnavailable },
			want: ErrOfflineUnavailable, status: 503, code: "DEPENDENCY_UNAVAILABLE"},
		{name: "relationship graph down", mutate: func(r *offlineRig) { *r.relErr = errors.New("graph down") },
			want: ErrOfflineUnavailable, status: 503, code: "DEPENDENCY_UNAVAILABLE"},
		{name: "account gate cannot ask the graph", mutate: func(r *offlineRig) { r.svc.graphServiceURL = "http://127.0.0.1:1" },
			want: ErrOfflineUnavailable, status: 503, code: "DEPENDENCY_UNAVAILABLE"},
		{name: "hidden-author list unreadable", mutate: func(r *offlineRig) { r.hidden.err = errors.New("db down") },
			want: ErrOfflineUnavailable, status: 503, code: "DEPENDENCY_UNAVAILABLE"},
		{name: "store cannot load the post", mutate: func(r *offlineRig) { r.store.postsErr = errors.New("db down") },
			want: ErrOfflineUnavailable, status: 503, code: "DEPENDENCY_UNAVAILABLE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newOfflineRig(t)
			if tc.mutate != nil {
				tc.mutate(r)
			}
			viewer := r.viewer
			if tc.viewer != nil {
				viewer = tc.viewer(r)
			}
			card, _, err := r.grant(viewer)
			if card != nil {
				t.Fatalf("a card was handed out: %+v", card)
			}
			wantOfflineErr(t, err, tc.want, tc.status, tc.code)
			if len(r.store.rows) != 0 {
				t.Fatalf("a refused grant wrote a row: %+v", r.store.rows)
			}
		})
	}
}

func TestOfflineGrantAllowsTheViewersPlaybackAllows(t *testing.T) {
	tier := uuid.New()
	cases := map[string]func(r *offlineRig) uuid.UUID{
		"unlisted": func(r *offlineRig) uuid.UUID { r.p().Visibility = "unlisted"; return r.viewer },
		"followers-only, a follower": func(r *offlineRig) uuid.UUID {
			r.p().Visibility = "followers"
			r.rels[r.author.String()] = ViewerRelationship{Follows: true}
			return r.viewer
		},
		"private, shared with the viewer": func(r *offlineRig) uuid.UUID {
			r.p().Visibility = "private"
			r.shares.shared[r.post] = map[uuid.UUID]bool{r.viewer: true}
			return r.viewer
		},
		"age-restricted, an adult": func(r *offlineRig) uuid.UUID { r.p().AgeRestricted = true; return r.viewer },
		"members-only, a member": func(r *offlineRig) uuid.UUID {
			r.p().TierRequiredID = &tier
			r.entitled[r.viewer] = true
			return r.viewer
		},
		"a reel":                      func(r *offlineRig) uuid.UUID { r.p().ContentType = "flick"; return r.viewer },
		"legacy reel content type":    func(r *offlineRig) uuid.UUID { r.p().ContentType = "reel"; return r.viewer },
		"legacy video content type":   func(r *offlineRig) uuid.UUID { r.p().ContentType = "video"; return r.viewer },
		"owner, downloads turned off": func(r *offlineRig) uuid.UUID { r.p().AllowDownload = false; return r.author },
		"owner, private":              func(r *offlineRig) uuid.UUID { r.p().Visibility = "private"; return r.author },
		"owner, members-only":         func(r *offlineRig) uuid.UUID { r.p().TierRequiredID = &tier; return r.author },
		"owner, pending review":       func(r *offlineRig) uuid.UUID { r.p().ReviewStatus = "pending"; return r.author },
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			r := newOfflineRig(t)
			viewer := setup(r)
			card, created, err := r.grant(viewer)
			if err != nil || !created || card == nil || card.Media.Variant != "720p" {
				t.Fatalf("grant: card=%+v created=%v err=%v", card, created, err)
			}
		})
	}
}

func TestOfflineGrantDeviceID(t *testing.T) {
	r := newOfflineRig(t)
	for _, bad := range []string{"", "   ", strings.Repeat("a", 65), "has space", "tab\there", "ctl\x00", "\xff\xfe"} {
		_, _, err := r.svc.GrantOfflineCopy(context.Background(), r.viewer, r.post, bad)
		wantOfflineErr(t, err, ErrOfflineDevice, 422, "INVALID_DEVICE")
	}
	if len(r.store.rows) != 0 {
		t.Fatalf("rows written for a bad device id: %d", len(r.store.rows))
	}
	for _, good := range []string{strings.Repeat("a", 64), "  padded-id  ", "9f1c2b7e-0b1f-4c53-9d0e-8a1f0e6a2c11", "tél-éphone"} {
		if _, _, err := r.svc.GrantOfflineCopy(context.Background(), r.viewer, r.post, good); err != nil {
			t.Fatalf("device %q refused: %v", good, err)
		}
	}
	if r.store.rows[offlineKey{r.viewer, r.post, "padded-id"}] == nil {
		t.Fatal("the device id was not trimmed before it was stored")
	}
}

func TestOfflineLimitIsOneHundredActiveCopiesPerUser(t *testing.T) {
	r := newOfflineRig(t)
	// 99 active copies of other posts, across two devices: the 100th is
	// still granted. The number is written out here on purpose — the
	// contract pins 100, and a test that read the constant would follow it
	// wherever it went.
	for i := 0; i < 99; i++ {
		device := offlineDevice
		if i%2 == 1 {
			device = "device-phone-0002"
		}
		other := uuid.New()
		r.store.rows[offlineKey{r.viewer, other, device}] = &postgres.OfflineCopy{
			UserID: r.viewer, PostID: other, DeviceID: device, GrantedAt: r.now, ExpiresAt: r.now.Add(time.Hour),
		}
	}
	if _, created, err := r.grant(r.viewer); err != nil || !created {
		t.Fatalf("the 100th copy: created=%v err=%v", created, err)
	}
	delete(r.store.rows, offlineKey{r.viewer, r.post, offlineDevice})
	// 100 active copies of other posts: the 101st is refused.
	for i := 0; i < 1; i++ {
		device := offlineDevice
		if i%2 == 1 {
			device = "device-phone-0002"
		}
		other := uuid.New()
		r.store.rows[offlineKey{r.viewer, other, device}] = &postgres.OfflineCopy{
			UserID: r.viewer, PostID: other, DeviceID: device, GrantedAt: r.now, ExpiresAt: r.now.Add(time.Hour),
		}
	}
	_, _, err := r.grant(r.viewer)
	wantOfflineErr(t, err, ErrOfflineLimit, 409, "OFFLINE_LIMIT")
	if r.row(r.viewer) != nil {
		t.Fatal("the 101st copy was written")
	}
	// Someone else is not held to this user's count.
	if _, _, err := r.grant(r.minor); err != nil {
		t.Fatalf("another user was refused: %v", err)
	}

	// A revoked or expired copy frees its slot.
	var one offlineKey
	for k := range r.store.rows {
		if k.user == r.viewer {
			one = k
			break
		}
	}
	revokedAt := r.now
	r.store.rows[one].RevokedAt = &revokedAt
	if _, created, err := r.grant(r.viewer); err != nil || !created {
		t.Fatalf("grant after a slot was freed: created=%v err=%v", created, err)
	}
	// Now exactly at the limit again: refreshing a copy the user already
	// holds is not a 101st copy.
	if _, created, err := r.grant(r.viewer); err != nil || created {
		t.Fatalf("refresh at the limit: created=%v err=%v", created, err)
	}
	// The same post on another device is another copy.
	_, _, err = r.svc.GrantOfflineCopy(context.Background(), r.viewer, r.post, "device-tablet-0003")
	wantOfflineErr(t, err, ErrOfflineLimit, 409, "OFFLINE_LIMIT")

	// An expired copy does not count either.
	for k, row := range r.store.rows {
		if k.user == r.viewer && k.post != r.post && row.RevokedAt == nil {
			row.ExpiresAt = r.now.Add(-time.Second)
			break
		}
	}
	if _, created, err := r.svc.GrantOfflineCopy(context.Background(), r.viewer, r.post, "device-tablet-0003"); err != nil || !created {
		t.Fatalf("grant after a copy expired: created=%v err=%v", created, err)
	}
}

func TestOfflineCardCarriesTheSoundAViewerMayHear(t *testing.T) {
	r := newOfflineRig(t)
	sound, soundMedia := uuid.New(), uuid.New()
	start := 1500
	r.p().ContentType = "flick"
	r.p().AudioTrackID, r.p().AudioStartMs = &sound, &start
	r.p().OriginalAudioVol, r.p().OverlayAudioVol = 0, 0.8
	r.svc.soundReads = &fakeSoundStore{tracks: map[uuid.UUID]*postgres.AudioTrack{
		sound: {ID: sound, Title: "Original sound", DurationMs: 28400, MediaID: &soundMedia, Status: "ready", IsPublic: true},
	}}
	may := true
	r.svc.soundAudience = func(_ context.Context, _ uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
		return map[uuid.UUID]bool{soundMedia: may}, nil
	}
	card := r.mustGrant(t, r.viewer)
	want := OfflineSound{Path: "/v1/audio/" + sound.String() + "/serve", Mime: "audio/mp4", StartMs: 1500, OriginalVolume: 0, OverlayVolume: 0.8}
	if card.Sound == nil || *card.Sound != want || card.ContentType != "flick" {
		t.Fatalf("sound = %+v, want %+v (content_type %q)", card.Sound, want, card.ContentType)
	}
	// On the wire: both volumes are present although one is 0 (0 is
	// "muted", not "unknown"), and no size is claimed for the sound.
	b, _ := json.Marshal(card.Sound)
	if got := string(b); !strings.Contains(got, `"original_volume":0,`) || !strings.Contains(got, `"overlay_volume":0.8`) || strings.Contains(got, "size_bytes") {
		t.Fatalf("sound on the wire: %s", got)
	}
	// A sound whose source the viewer may no longer hear is left off.
	may = false
	if card := r.mustGrant(t, r.viewer); card.Sound != nil {
		t.Fatalf("a sound the viewer may not hear is on the card: %+v", card.Sound)
	}
}

func TestOfflineCaptionsAreBestEffortAndNeverNull(t *testing.T) {
	r := newOfflineRig(t)
	r.captions.tracks, r.captions.err = nil, errors.New("media-service down")
	card := r.mustGrant(t, r.viewer)
	if card.Captions == nil || len(card.Captions) != 0 {
		t.Fatalf("captions = %#v, want an empty list", card.Captions)
	}
	if b, _ := json.Marshal(card); !strings.Contains(string(b), `"captions":[]`) || !strings.Contains(string(b), `"sound":null`) {
		t.Fatalf("wire shape: %s", b)
	}
}

func TestOfflinePosterFallsBackToTheVideoThumbnail(t *testing.T) {
	r := newOfflineRig(t)
	r.p().CoverMediaID = nil
	if card := r.mustGrant(t, r.viewer); card.PosterPath != "/v1/media/"+r.video.String()+"/serve/thumb_150" {
		t.Fatalf("poster = %q", card.PosterPath)
	}
	r.media.records[r.video].Variants = r.media.records[r.video].Variants[1:] // no thumbnail
	if card := r.mustGrant(t, r.viewer); card.PosterPath != "" {
		t.Fatalf("poster = %q, want none", card.PosterPath)
	}
}

// ── the check ──────────────────────────────────────────────────────────────

func TestOfflineCheckValidCopyIsStampedAndNotExtended(t *testing.T) {
	r := newOfflineRig(t)
	card := r.mustGrant(t, r.viewer)
	r.now = r.now.Add(48 * time.Hour)
	items := r.check(t, r.viewer, r.post.String())
	if len(items) != 1 || !items[0].Valid || items[0].Reason != "" || items[0].PostID != r.post.String() || items[0].ContentType != "long_video" {
		t.Fatalf("items = %+v", items)
	}
	if items[0].ExpiresAt == nil || !items[0].ExpiresAt.Equal(card.ExpiresAt) {
		t.Fatalf("a check moved the expiry: %v, granted until %v", items[0].ExpiresAt, card.ExpiresAt)
	}
	row := r.row(r.viewer)
	if !row.ExpiresAt.Equal(card.ExpiresAt) {
		t.Fatalf("stored expiry moved: %v", row.ExpiresAt)
	}
	if row.LastCheckedAt == nil || !row.LastCheckedAt.Equal(r.now) {
		t.Fatalf("last_checked_at = %v, want %v", row.LastCheckedAt, r.now)
	}
}

func TestOfflineCheckComputesLiveAndNeverTrustsTheRow(t *testing.T) {
	tier := uuid.New()
	cases := []struct {
		name    string
		after   func(r *offlineRig)
		reason  string
		revoked string // the reason written to the row; "" = row untouched
	}{
		{"post deleted", func(r *offlineRig) { r.p().DeletedAt = dayp(2026, 10, 2) }, "deleted", "deleted"},
		{"post hard-deleted", func(r *offlineRig) { delete(r.store.posts, r.post) }, "deleted", "deleted"},
		{"made private", func(r *offlineRig) { r.p().Visibility = "private" }, "private", "private"},
		{"made followers-only", func(r *offlineRig) { r.p().Visibility = "followers" }, "private", "private"},
		{"author went private", func(r *offlineRig) { r.p().AuthorID = r.private }, "private", "private"},
		{"author hidden", func(r *offlineRig) { r.hidden.hidden[r.author] = true }, "private", "private"},
		{"taken down in review", func(r *offlineRig) { r.p().ReviewStatus = "rejected" }, "private", "private"},
		{"downloads turned off", func(r *offlineRig) { r.p().AllowDownload = false }, "not_allowed", "not_allowed"},
		{"membership lapsed", func(r *offlineRig) { r.p().TierRequiredID = &tier }, "not_allowed", "not_allowed"},
		{"viewer blocked the author", func(r *offlineRig) { r.rels[r.author.String()] = ViewerRelationship{Blocked: true} }, "blocked", "blocked"},
		{"author blocked the viewer", func(r *offlineRig) { r.rels[r.author.String()] = ViewerRelationship{BlockedBy: true} }, "blocked", "blocked"},
		{"expired", func(r *offlineRig) { r.now = r.now.Add(30*24*time.Hour + time.Second) }, "expired", ""},
		{"expires this instant", func(r *offlineRig) { r.now = r.now.Add(30 * 24 * time.Hour) }, "expired", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newOfflineRig(t)
			r.mustGrant(t, r.viewer)
			tc.after(r)
			items := r.check(t, r.viewer, r.post.String())
			if len(items) != 1 || items[0].Valid || items[0].Reason != tc.reason || items[0].ExpiresAt != nil || items[0].ContentType != "" {
				t.Fatalf("items = %+v, want invalid %q", items, tc.reason)
			}
			row := r.row(r.viewer)
			if got := row.RevokeReason; got != tc.revoked || (tc.revoked != "") != (row.RevokedAt != nil) {
				t.Fatalf("row revoke = %q at %v, want %q", got, row.RevokedAt, tc.revoked)
			}
			if len(r.store.touched) != 0 {
				t.Fatalf("an invalid copy was stamped as checked: %v", r.store.touched)
			}
		})
	}
}

// recheck_after_seconds is advice to the client. A device that was offline
// far longer than that and then asks is answered normally: nothing is
// revoked, and nothing is invalid, just because the check is overdue.
func TestOfflineCheckOverdueRecheckIsAnsweredNormally(t *testing.T) {
	r := newOfflineRig(t)
	card := r.mustGrant(t, r.viewer)
	if last := r.row(r.viewer).LastCheckedAt; last == nil || !last.Equal(r.now) {
		t.Fatalf("last_checked_at after the grant = %v", last)
	}
	// 20 days of silence: ten times the recheck interval, inside the 30 days.
	r.now = r.now.Add(20 * 24 * time.Hour)
	if overdue := r.now.Sub(*r.row(r.viewer).LastCheckedAt); overdue <= OfflineRecheckAfterSeconds*time.Second {
		t.Fatalf("the test is not overdue: %v", overdue)
	}
	items := r.check(t, r.viewer, r.post.String())
	if !items[0].Valid || items[0].Reason != "" || !items[0].ExpiresAt.Equal(card.ExpiresAt) {
		t.Fatalf("an overdue check was not answered normally: %+v", items)
	}
	row := r.row(r.viewer)
	if row.RevokedAt != nil || row.RevokeReason != "" || !row.ExpiresAt.Equal(card.ExpiresAt) || !row.LastCheckedAt.Equal(r.now) {
		t.Fatalf("an overdue check changed the copy: %+v", row)
	}
	// The list answers the same way.
	if cards, err := r.svc.ListOfflineCopies(context.Background(), r.viewer, offlineDevice); err != nil || len(cards) != 1 {
		t.Fatalf("list after an overdue check: %+v %v", cards, err)
	}
	// Only the 30 days end a copy by time.
	r.now = card.ExpiresAt.Add(time.Second)
	if items := r.check(t, r.viewer, r.post.String()); items[0].Valid || items[0].Reason != "expired" {
		t.Fatalf("past the expiry: %+v", items)
	}
	if row := r.row(r.viewer); row.RevokedAt != nil {
		t.Fatalf("an expired copy was marked revoked: %+v", row)
	}
}

func TestOfflineCheckRevokedRowStaysRevokedWhenThePostIsFine(t *testing.T) {
	r := newOfflineRig(t)
	r.mustGrant(t, r.viewer)
	if _, err := r.svc.RemoveOfflineCopy(context.Background(), r.viewer, r.post, offlineDevice); err != nil {
		t.Fatal(err)
	}
	items := r.check(t, r.viewer, r.post.String())
	if items[0].Valid || items[0].Reason != "revoked" {
		t.Fatalf("items = %+v", items)
	}
	// Downloads were turned off and on again: the copy that was revoked in
	// between does not come back by itself.
	r2 := newOfflineRig(t)
	r2.mustGrant(t, r2.viewer)
	r2.p().AllowDownload = false
	r2.check(t, r2.viewer, r2.post.String())
	r2.p().AllowDownload = true
	if items := r2.check(t, r2.viewer, r2.post.String()); items[0].Valid || items[0].Reason != "revoked" {
		t.Fatalf("items = %+v, want revoked", items)
	}
	// Saving it again does.
	if _, created, err := r2.grant(r2.viewer); err != nil || !created {
		t.Fatalf("re-grant: created=%v err=%v", created, err)
	}
	if items := r2.check(t, r2.viewer, r2.post.String()); !items[0].Valid {
		t.Fatalf("items = %+v, want valid after a new grant", items)
	}
}

func TestOfflineCheckOwnersCopySurvivesTheirOwnSwitches(t *testing.T) {
	r := newOfflineRig(t)
	r.mustGrant(t, r.author)
	r.p().AllowDownload = false
	r.p().Visibility = "private"
	if items := r.check(t, r.author, r.post.String()); !items[0].Valid {
		t.Fatalf("the owner's copy was invalidated: %+v", items)
	}
}

func TestOfflineCheckUnknownOrderAndDuplicates(t *testing.T) {
	r := newOfflineRig(t)
	r.mustGrant(t, r.viewer)
	never := uuid.New()
	items := r.check(t, r.viewer, "not-a-uuid", r.post.String(), never.String(), strings.ToUpper(r.post.String()), uuid.Nil.String())
	if len(items) != 4 {
		t.Fatalf("items = %+v, want 4 (the duplicate once)", items)
	}
	want := []OfflineCheckItem{
		{PostID: "not-a-uuid", Reason: "unknown"},
		{PostID: r.post.String(), Valid: true},
		{PostID: never.String(), Reason: "unknown"},
		{PostID: uuid.Nil.String(), Reason: "unknown"},
	}
	for i := range want {
		if items[i].PostID != want[i].PostID || items[i].Valid != want[i].Valid || items[i].Reason != want[i].Reason {
			t.Fatalf("item %d = %+v, want %+v", i, items[i], want[i])
		}
	}
	// Another device holds no copy of it, and neither does another user.
	other, err := r.svc.CheckOfflineCopies(context.Background(), r.viewer, "device-phone-0002", []string{r.post.String()})
	if err != nil || other[0].Valid || other[0].Reason != "unknown" {
		t.Fatalf("other device: %+v %v", other, err)
	}
	if items := r.check(t, r.minor, r.post.String()); items[0].Valid || items[0].Reason != "unknown" {
		t.Fatalf("other user: %+v", items)
	}
}

func TestOfflineCheckCapAndDevice(t *testing.T) {
	r := newOfflineRig(t)
	ids := make([]string, MaxOfflineCheckIDs+1)
	for i := range ids {
		ids[i] = uuid.NewString()
	}
	_, err := r.svc.CheckOfflineCopies(context.Background(), r.viewer, offlineDevice, ids)
	wantOfflineErr(t, err, ErrOfflineTooMany, 422, "INVALID_REQUEST")
	if items, err := r.svc.CheckOfflineCopies(context.Background(), r.viewer, offlineDevice, ids[:MaxOfflineCheckIDs]); err != nil || len(items) != MaxOfflineCheckIDs {
		t.Fatalf("100 ids: %d items, err %v", len(items), err)
	}
	_, err = r.svc.CheckOfflineCopies(context.Background(), r.viewer, "", []string{r.post.String()})
	wantOfflineErr(t, err, ErrOfflineDevice, 422, "INVALID_DEVICE")
	if items, err := r.svc.CheckOfflineCopies(context.Background(), r.viewer, offlineDevice, nil); err != nil || len(items) != 0 {
		t.Fatalf("empty list: %+v %v", items, err)
	}
}

// An outage is a retry. It is never an "invalid", because a client deletes
// the stored file when it is told a copy is invalid.
func TestOfflineCheckOutageIsNeverAnInvalidCopy(t *testing.T) {
	tier := uuid.New()
	cases := map[string]func(r *offlineRig){
		"relationship graph down": func(r *offlineRig) { *r.relErr = errors.New("graph down") },
		"account gate cannot ask the graph": func(r *offlineRig) {
			r.svc.graphServiceURL = "http://127.0.0.1:1"
			r.svc.privacyCache = nil // the grant's answer is 3 s fresh; the outage outlasts it
		},
		"hidden-author list unreadable": func(r *offlineRig) { r.hidden.err = errors.New("db down") },
		"monetization down":             func(r *offlineRig) { r.p().TierRequiredID = &tier; r.entErr = errors.New("down") },
		"posts unreadable":              func(r *offlineRig) { r.store.postsErr = errors.New("db down") },
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			r := newOfflineRig(t)
			r.mustGrant(t, r.viewer)
			breakIt(r)
			items, err := r.svc.CheckOfflineCopies(context.Background(), r.viewer, offlineDevice, []string{r.post.String()})
			if items != nil {
				t.Fatalf("an outage produced answers: %+v", items)
			}
			wantOfflineErr(t, err, ErrOfflineUnavailable, 503, "DEPENDENCY_UNAVAILABLE")
			if row := r.row(r.viewer); row.RevokedAt != nil {
				t.Fatalf("an outage revoked the copy: %+v", row)
			}
			if _, err := r.svc.ListOfflineCopies(context.Background(), r.viewer, offlineDevice); !errors.Is(err, ErrOfflineUnavailable) {
				t.Fatalf("list during an outage: %v", err)
			}
		})
	}
}

// ── the list and the removal ───────────────────────────────────────────────

func TestOfflineListIsTheGrantWithoutThePath(t *testing.T) {
	r := newOfflineRig(t)
	granted := r.mustGrant(t, r.viewer)

	second, secondVideo := uuid.New(), uuid.New()
	r.store.posts[second] = &postgres.Post{ID: second, AuthorID: r.author, Visibility: "public", ReviewStatus: "approved",
		ContentType: "flick", AllowDownload: true, Text: "first line\nsecond", Media: []postgres.PostMedia{{MediaID: secondVideo, Kind: "video"}}}
	r.states[secondVideo] = postgres.MediaOwnership{UploaderID: r.author, Kind: "video", ProcessingStatus: "ready", ModerationStatus: "passed", DurationMs: 21000}
	r.media.records[secondVideo] = &FeedMediaRecord{FileType: "video", ProcessingStatus: "ready", ModerationStatus: "passed",
		Variants: []FeedMediaVariant{{Name: "480p", Mime: "video/mp4", SizeBytes: ip64(5_000_000), ObjectKey: "k2/480"}}}
	r.now = r.now.Add(time.Minute)
	if _, _, err := r.svc.GrantOfflineCopy(context.Background(), r.viewer, second, offlineDevice); err != nil {
		t.Fatal(err)
	}
	// A copy on another device, and another user's copy, are not this list's.
	if _, _, err := r.svc.GrantOfflineCopy(context.Background(), r.viewer, r.post, "device-phone-0002"); err != nil {
		t.Fatal(err)
	}
	r.mustGrant(t, r.minor)

	cards, err := r.svc.ListOfflineCopies(context.Background(), r.viewer, offlineDevice)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 2 || cards[0].PostID != second || cards[1].PostID != r.post {
		t.Fatalf("cards = %+v, want the newer grant first", cards)
	}
	if cards[0].Title != "first line" || cards[0].ContentType != "flick" || cards[1].ContentType != "long_video" ||
		cards[0].DurationMs != 21000 || cards[0].Media.Variant != "480p" || cards[0].Media.SizeBytes != 5_000_000 {
		t.Fatalf("reel card = %+v", cards[0])
	}
	wantMedia := granted.Media
	wantMedia.Path = ""
	got := cards[1]
	if got.Media != wantMedia || got.Title != granted.Title || got.DurationMs != granted.DurationMs || got.PosterPath != granted.PosterPath ||
		!got.ExpiresAt.Equal(granted.ExpiresAt) || got.RecheckAfterSeconds != granted.RecheckAfterSeconds || len(got.Captions) != 1 {
		t.Fatalf("listed card = %+v, granted %+v", got, granted)
	}
	b, _ := json.Marshal(cards)
	if strings.Contains(string(b), `"path":"/v1/media`) || strings.Contains(string(b), "/serve/720p") {
		t.Fatalf("the list hands out a media path: %s", b)
	}

	// The list is computed live too: a copy that is no longer valid is gone
	// from it, and so is a removed and an expired one.
	r.p().AllowDownload = false
	cards, err = r.svc.ListOfflineCopies(context.Background(), r.viewer, offlineDevice)
	if err != nil || len(cards) != 1 || cards[0].PostID != second {
		t.Fatalf("after downloads off: %+v %v", cards, err)
	}
	if row := r.row(r.viewer); row.RevokedAt == nil || row.RevokeReason != "not_allowed" {
		t.Fatalf("row = %+v", row)
	}
	r.now = r.now.Add(31 * 24 * time.Hour)
	if cards, _ := r.svc.ListOfflineCopies(context.Background(), r.viewer, offlineDevice); len(cards) != 0 {
		t.Fatalf("expired copies are listed: %+v", cards)
	}
	if _, err := r.svc.ListOfflineCopies(context.Background(), r.viewer, " "); !errors.Is(err, ErrOfflineDevice) {
		t.Fatalf("list without a device: %v", err)
	}
}

func TestOfflineRemoveIsIdempotentAndUngated(t *testing.T) {
	r := newOfflineRig(t)
	r.mustGrant(t, r.viewer)
	r.p().Visibility = "private" // no longer the viewer's to watch: still removable
	for i := 0; i < 2; i++ {
		res, err := r.svc.RemoveOfflineCopy(context.Background(), r.viewer, r.post, offlineDevice)
		if err != nil || res == nil || !res.Removed || res.PostID != r.post {
			t.Fatalf("remove %d: %+v %v", i, res, err)
		}
	}
	row := r.row(r.viewer)
	if row == nil || row.RevokedAt == nil || row.RevokeReason != "removed" {
		t.Fatalf("the row was deleted or not revoked: %+v", row)
	}
	// A copy that never existed is the same 200.
	if res, err := r.svc.RemoveOfflineCopy(context.Background(), r.viewer, uuid.New(), offlineDevice); err != nil || !res.Removed {
		t.Fatalf("remove of nothing: %+v %v", res, err)
	}
	if _, err := r.svc.RemoveOfflineCopy(context.Background(), r.viewer, r.post, ""); !errors.Is(err, ErrOfflineDevice) {
		t.Fatalf("remove without a device: %v", err)
	}
	// Removing one device's copy leaves another device's alone.
	r2 := newOfflineRig(t)
	r2.mustGrant(t, r2.viewer)
	if _, _, err := r2.svc.GrantOfflineCopy(context.Background(), r2.viewer, r2.post, "device-phone-0002"); err != nil {
		t.Fatal(err)
	}
	if _, err := r2.svc.RemoveOfflineCopy(context.Background(), r2.viewer, r2.post, "device-phone-0002"); err != nil {
		t.Fatal(err)
	}
	if items := r2.check(t, r2.viewer, r2.post.String()); !items[0].Valid {
		t.Fatalf("the other device's copy went with it: %+v", items)
	}
}

func TestOfflineUnwiredServiceIsUnavailableNotAllowed(t *testing.T) {
	s := &Service{}
	ctx := context.Background()
	if _, _, err := s.GrantOfflineCopy(ctx, uuid.New(), uuid.New(), offlineDevice); !errors.Is(err, ErrOfflineUnavailable) {
		t.Fatalf("grant: %v", err)
	}
	if _, err := s.CheckOfflineCopies(ctx, uuid.New(), offlineDevice, nil); !errors.Is(err, ErrOfflineUnavailable) {
		t.Fatalf("check: %v", err)
	}
	if _, err := s.ListOfflineCopies(ctx, uuid.New(), offlineDevice); !errors.Is(err, ErrOfflineUnavailable) {
		t.Fatalf("list: %v", err)
	}
	if _, err := s.RemoveOfflineCopy(ctx, uuid.New(), uuid.New(), offlineDevice); !errors.Is(err, ErrOfflineUnavailable) {
		t.Fatalf("remove: %v", err)
	}
}

// ── the caption client ─────────────────────────────────────────────────────

func TestOfflineCaptionClientAsksAsTheViewerAndDropsDrafts(t *testing.T) {
	viewer, media := uuid.New(), uuid.New()
	var gotViewer, gotKey, gotPath string
	no := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotViewer, gotKey, gotPath = r.Header.Get("X-User-Id"), r.Header.Get("X-Internal-Service-Key"), r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"subtitles": []map[string]any{
			{"language": "en", "source": "manual", "published": true, "content": "WEBVTT ..."},
			{"language": "hi", "source": "auto_generated", "published": true},
			{"language": "te", "source": "auto_generated", "published": &no},
			{"language": "en", "source": "manual", "published": true},
			{"language": "../../etc", "source": "manual", "published": true},
			{"language": "pt-BR", "source": "translated"},
		}}})
	}))
	defer srv.Close()
	s := &Service{mediaServiceURL: srv.URL + "/", internalServiceKey: "test-key", httpClient: http.DefaultClient}
	tracks, err := httpOfflineCaptionSource{svc: s}.CaptionTracks(context.Background(), viewer, media)
	if err != nil {
		t.Fatal(err)
	}
	if gotViewer != viewer.String() || gotKey != "test-key" || gotPath != "/v1/subtitles/"+media.String() {
		t.Fatalf("request: viewer=%q key=%q path=%q", gotViewer, gotKey, gotPath)
	}
	base := "/v1/subtitles/" + media.String() + "/track/"
	want := []OfflineCaption{
		{Lang: "en", Label: "English", Path: base + "en.vtt"},
		{Lang: "hi", Label: "Hindi (auto-generated)", Path: base + "hi.vtt"},
		{Lang: "pt-BR", Label: "Portuguese", Path: base + "pt-BR.vtt"},
	}
	if len(tracks) != len(want) {
		t.Fatalf("tracks = %+v", tracks)
	}
	for i := range want {
		if tracks[i] != want[i] {
			t.Fatalf("track %d = %+v, want %+v", i, tracks[i], want[i])
		}
	}

	// A refusal or an outage is an error (the grant then lists no captions).
	deny := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	defer deny.Close()
	s.mediaServiceURL = deny.URL
	if _, err := (httpOfflineCaptionSource{svc: s}).CaptionTracks(context.Background(), viewer, media); err == nil {
		t.Fatal("a 404 from media-service was read as a caption list")
	}
	if _, err := (httpOfflineCaptionSource{svc: &Service{}}).CaptionTracks(context.Background(), viewer, media); err == nil {
		t.Fatal("an unconfigured media-service was read as a caption list")
	}
}

func TestOfflineCaptionLabel(t *testing.T) {
	for in, want := range map[[2]string]string{
		{"en", "manual"}: "English", {"te", "auto_generated"}: "Telugu (auto-generated)",
		{"zh-Hant", "manual"}: "Chinese", {"xx", "manual"}: "xx", {"EN", "translated"}: "English",
	} {
		if got := offlineCaptionLabel(in[0], in[1]); got != want {
			t.Errorf("label(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

// The playback decision and the offline decision are one function: the
// verdict's `allowed` is what postMediaJudge returns, for every viewer.
func TestOfflineUsesThePlaybackDecision(t *testing.T) {
	r := newOfflineRig(t)
	mutations := map[string]func(p *postgres.Post){
		"public": func(*postgres.Post) {}, "private": func(p *postgres.Post) { p.Visibility = "private" },
		"followers": func(p *postgres.Post) { p.Visibility = "followers" }, "scheduled": func(p *postgres.Post) { p.PublishAt = dayp(2030, 1, 1) },
		"pending": func(p *postgres.Post) { p.ReviewStatus = "pending" }, "18+": func(p *postgres.Post) { p.AgeRestricted = true },
	}
	base := *r.p()
	for name, mutate := range mutations {
		for _, viewer := range []uuid.UUID{r.viewer, r.minor, r.author} {
			p := base
			mutate(&p)
			judge, err := r.svc.postMediaJudge(context.Background(), viewer, []*postgres.Post{&p})
			if err != nil {
				t.Fatal(err)
			}
			r.store.posts[r.post] = &p
			r.store.rows = map[offlineKey]*postgres.OfflineCopy{}
			_, _, gerr := r.grant(viewer)
			granted := gerr == nil
			// The owner's scheduled post plays for them but is not ready to copy.
			if viewer == r.author && name == "scheduled" {
				if !errors.Is(gerr, ErrOfflineNotReady) {
					t.Fatalf("%s/owner: %v", name, gerr)
				}
				continue
			}
			if granted != judge(&p) {
				t.Fatalf("%s viewer=%v: playback=%v offline=%v (%v)", name, viewer, judge(&p), granted, gerr)
			}
		}
	}
}

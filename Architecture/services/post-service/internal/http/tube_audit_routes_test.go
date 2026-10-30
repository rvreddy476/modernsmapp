package http

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// The tube backend audit (2026-10-30) at the routes: the wire shapes and
// statuses of its fixes. The decisions themselves are pinned in
// internal/service/tube_audit_test.go.

type tubePlaylistItems struct {
	gotPosition *int
	calls       int
}

func (f *tubePlaylistItems) AddPlaylistItem(_ context.Context, playlistID, postID uuid.UUID, position *int, _ int) (*postgres.PlaylistItem, error) {
	f.calls++
	f.gotPosition = position
	at := 4
	if position != nil {
		at = *position
	}
	return &postgres.PlaylistItem{PlaylistID: playlistID, PostID: postID, Position: at}, nil
}

type tubeChapters struct {
	stored []postgres.MediaChapter
	saves  int
}

func (f *tubeChapters) SaveChapters(_ context.Context, _ uuid.UUID, ch []postgres.MediaChapter) error {
	f.saves++
	f.stored = ch
	return nil
}
func (f *tubeChapters) GetChapters(context.Context, uuid.UUID) ([]postgres.MediaChapter, error) {
	return f.stored, nil
}

type tubeRig struct {
	*gateRig
	owner, post uuid.UUID
	playlist    *postgres.Playlist
	items       *tubePlaylistItems
	chapters    *tubeChapters
	tier        *uuid.UUID
	auth        *esAuthoring
}

func newTubeRig(t *testing.T) *tubeRig {
	t.Helper()
	r := &tubeRig{owner: uuid.New(), post: uuid.New(), items: &tubePlaylistItems{},
		chapters: &tubeChapters{stored: []postgres.MediaChapter{{ChapterIndex: 0, Title: "intro"}}}}
	r.playlist = &postgres.Playlist{ID: uuid.New(), CreatorID: r.owner, Title: "p", Visibility: "public", Kind: postgres.PlaylistKindUser}
	auth := &esAuthoring{
		authors:   map[uuid.UUID]uuid.UUID{r.post: r.owner},
		playlists: map[uuid.UUID]*postgres.Playlist{r.playlist.ID: r.playlist},
		items: map[uuid.UUID][]postgres.PlaylistItem{r.playlist.ID: {
			{PlaylistID: r.playlist.ID, PostID: uuid.New(), Position: 0},
			{PlaylistID: r.playlist.ID, PostID: uuid.New(), Position: 1},
		}},
	}
	r.auth = auth
	r.gateRig = newGateRig(t, func(uuid.UUID, *uuid.UUID) error { return nil }, func(d *service.HandlerTestDeps) {
		d.Authoring = auth
		d.PlaylistItems = r.items
		d.Chapters = r.chapters
		d.PostGate = func(context.Context, uuid.UUID) (uuid.UUID, *uuid.UUID, error) { return r.owner, r.tier, nil }
		d.BatchPosts = func(context.Context, []uuid.UUID, *uuid.UUID) (map[uuid.UUID]*service.PostDetail, error) {
			return map[uuid.UUID]*service.PostDetail{}, nil
		}
	})
	return r
}

type tubeEnvelope struct {
	Data json.RawMessage `json:"data"`
	Meta *struct {
		NextCursor string `json:"next_cursor"`
	} `json:"meta"`
	Error *struct {
		Code string `json:"code"`
	} `json:"error"`
}

func decodeTube(t *testing.T, body []byte) tubeEnvelope {
	t.Helper()
	var e tubeEnvelope
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return e
}

// M1: an add without a position appends; it used to default to 0 and
// overwrite the first video.
func TestAddPlaylistItemWithoutPositionAppends(t *testing.T) {
	r := newTubeRig(t)
	w := r.do(http.MethodPost, "/v1/playlists/"+r.playlist.ID.String()+"/items", &r.owner, `{"post_id":"`+uuid.NewString()+`"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if r.items.gotPosition != nil {
		t.Fatalf("an add without a position reached the store at %d; want nil (append)", *r.items.gotPosition)
	}
	var data struct {
		Position int `json:"position"`
	}
	_ = json.Unmarshal(decodeTube(t, w.Body.Bytes()).Data, &data)
	if data.Position != 4 {
		t.Fatalf("answered position %d; want the one the row holds (4)", data.Position)
	}
	w = r.do(http.MethodPost, "/v1/playlists/"+r.playlist.ID.String()+"/items", &r.owner, `{"post_id":"`+uuid.NewString()+`","position":-2}`)
	if w.Code != http.StatusBadRequest || r.items.calls != 1 {
		t.Fatalf("a negative position: status %d, store calls %d; want 400 and no write", w.Code, r.items.calls)
	}
}

// M4: the items endpoint is paged with a cursor.
func TestPlaylistItemsRouteIsPaged(t *testing.T) {
	r := newTubeRig(t)
	w := r.do(http.MethodGet, "/v1/playlists/"+r.playlist.ID.String()+"/items?limit=2", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	e := decodeTube(t, w.Body.Bytes())
	if e.Meta == nil || e.Meta.NextCursor != "1" {
		t.Fatalf("a full page carries meta %+v; want next_cursor 1", e.Meta)
	}
	w = r.do(http.MethodGet, "/v1/playlists/"+r.playlist.ID.String()+"/items?cursor=abc", nil, "")
	if w.Code != http.StatusBadRequest || decodeTube(t, w.Body.Bytes()).Error.Code != "INVALID_CURSOR" {
		t.Fatalf("a forged cursor: status %d %s", w.Code, w.Body)
	}
}

// Clients released before paging send neither limit nor cursor and draw the
// response as the whole playlist: they get every item up to the legacy cap,
// read a page at a time, not just the first page.
func TestPlaylistItemsUnpagedReadReturnsTheWholePlaylist(t *testing.T) {
	r := newTubeRig(t)
	var many []postgres.PlaylistItem
	for i := 0; i < 250; i++ {
		many = append(many, postgres.PlaylistItem{PlaylistID: r.playlist.ID, PostID: uuid.New(), Position: i})
	}
	r.auth.items[r.playlist.ID] = many
	w := r.do(http.MethodGet, "/v1/playlists/"+r.playlist.ID.String()+"/items", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var got []json.RawMessage
	_ = json.Unmarshal(decodeTube(t, w.Body.Bytes()).Data, &got)
	if len(got) != 250 {
		t.Fatalf("an un-paged read returned %d of 250 items; older clients would lose the rest", len(got))
	}
	w = r.do(http.MethodGet, "/v1/playlists/"+r.playlist.ID.String()+"/items?limit=2", nil, "")
	_ = json.Unmarshal(decodeTube(t, w.Body.Bytes()).Data, &got)
	if len(got) != 2 {
		t.Fatalf("an explicit limit=2 returned %d items", len(got))
	}
}

// L2: a duplicate chapter index is a 400 that names it, not a 500.
func TestSaveChaptersDuplicateIndexIs400(t *testing.T) {
	r := newTubeRig(t)
	w := r.do(http.MethodPost, "/v1/posts/"+r.post.String()+"/chapters", &r.owner,
		`{"chapters":[{"chapter_index":0,"title":"a","start_ms":0},{"chapter_index":0,"title":"b","start_ms":5000}]}`)
	if w.Code != http.StatusBadRequest || decodeTube(t, w.Body.Bytes()).Error.Code != "DUPLICATE_CHAPTER_INDEX" {
		t.Fatalf("status %d %s; want 400 DUPLICATE_CHAPTER_INDEX", w.Code, w.Body)
	}
	if r.chapters.saves != 0 {
		t.Fatal("a refused list was written")
	}
	w = r.do(http.MethodPost, "/v1/posts/"+r.post.String()+"/chapters", &r.owner,
		`{"chapters":[{"chapter_index":0,"title":"a","start_ms":0,"thumbnail_url":"https://tracker.example/t.gif"}]}`)
	if w.Code != http.StatusBadRequest || decodeTube(t, w.Body.Bytes()).Error.Code != "INVALID_CHAPTERS" {
		t.Fatalf("an off-platform thumbnail: status %d %s; want 400 INVALID_CHAPTERS", w.Code, w.Body)
	}
}

// M2: GET chapters applies the members-only gate exactly as the detail does.
func TestGetChaptersOfAMembersOnlyVideo(t *testing.T) {
	r := newTubeRig(t)
	tier := uuid.New()
	r.tier = &tier
	w := r.do(http.MethodGet, "/v1/posts/"+r.post.String()+"/chapters", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var got []postgres.MediaChapter
	_ = json.Unmarshal(decodeTube(t, w.Body.Bytes()).Data, &got)
	if len(got) != 0 {
		t.Fatalf("a non-member read %d chapters of a members-only video", len(got))
	}
	w = r.do(http.MethodGet, "/v1/posts/"+r.post.String()+"/chapters", &r.owner, "")
	_ = json.Unmarshal(decodeTube(t, w.Body.Bytes()).Data, &got)
	if len(got) != 1 {
		t.Fatalf("the author read %d chapters; want theirs", len(got))
	}
}

// H2: adding someone else's video to your series is a 403.
func TestAddForeignEpisodeIs403(t *testing.T) {
	owner, victim := uuid.New(), uuid.New()
	vs := &postgres.VideoSeries{ID: uuid.New(), CreatorID: owner, Title: "S", IsPublic: true}
	store := &foreignAuthorSeries{gateSeriesStore: gateSeriesStore{series: vs}, author: victim}
	g := newGateRig(t, func(uuid.UUID, *uuid.UUID) error { return nil }, func(d *service.HandlerTestDeps) { d.VideoSeries = store })
	w := g.do(http.MethodPost, "/v1/video-series/"+vs.ID.String()+"/episodes", &owner,
		`{"post_id":"`+uuid.NewString()+`","episode_num":1}`)
	if w.Code != http.StatusForbidden || decodeTube(t, w.Body.Bytes()).Error.Code != "NOT_OWN_VIDEO" {
		t.Fatalf("status %d %s; want 403 NOT_OWN_VIDEO", w.Code, w.Body)
	}
}

type foreignAuthorSeries struct {
	gateSeriesStore
	author uuid.UUID
}

func (f *foreignAuthorSeries) GetPostAuthorID(context.Context, uuid.UUID) (uuid.UUID, error) {
	return f.author, nil
}

// L3: sent_at_ms orders reports, and a fast clock is taken as now.
func TestClampProgressSentAt(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	if clampProgressSentAt(nil, now) != nil {
		t.Fatal("absent sent_at_ms must leave the order unset")
	}
	zero := int64(0)
	if clampProgressSentAt(&zero, now) != nil {
		t.Fatal("a zero sent_at_ms must leave the order unset")
	}
	past := now.Add(-time.Minute).UnixMilli()
	if got := clampProgressSentAt(&past, now); got == nil || !got.Equal(now.Add(-time.Minute)) {
		t.Fatalf("a past time: %v", got)
	}
	future := now.Add(time.Hour).UnixMilli()
	if got := clampProgressSentAt(&future, now); got == nil || !got.Equal(now) {
		t.Fatalf("a time ahead of the server: %v; want now", got)
	}
}

package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"
)

// The tube / long-video backend audit (2026-10-30), its unit-level pins.
// The SQL halves are pinned against a real Postgres in the integration
// tests (tube_audit_integration_test.go, CI only).

// ── H1: composer drafts are held to CreatePost's title and text rules ──────

func TestDraftSaveRefusesAShapeThePublishWouldRefuse(t *testing.T) {
	long := strings.Repeat("a", MaxPostTextRunes+1)
	longTitle := strings.Repeat("t", MaxTitleRunes+1)
	for _, c := range []struct {
		name, kind, payload string
		want                error
	}{
		{"long video without a title", "video", `{"text":"x","content_type":"long_video","media_ids":["` + uuid.NewString() + `"]}`, ErrTitleRequired},
		{"legacy video spelling without a title", "post", `{"text":"x","content_type":"video"}`, ErrTitleRequired},
		{"title over the limit", "post", `{"text":"x","title":"` + longTitle + `"}`, ErrTitleTooLong},
		{"text over the limit", "post", `{"text":"` + long + `"}`, ErrTextTooLong},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := ValidateDraftPayload(c.kind, []byte(c.payload))
			if !errors.Is(err, c.want) || !errors.Is(err, ErrInvalidDraft) {
				t.Fatalf("err = %v, want ErrInvalidDraft wrapping %v", err, c.want)
			}
		})
	}
	// The rules' own boundary still saves.
	ok := `{"text":"` + strings.Repeat("a", MaxPostTextRunes) + `","content_type":"long_video","title":"Episode one"}`
	if _, err := ValidateDraftPayload("video", []byte(ok)); err != nil {
		t.Fatalf("a long video with a title and text at the limit: %v", err)
	}
}

func TestPostDraftBlockReasonParksRefusalsOfTheDraftOnly(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("create: %w", ErrTitleRequired), "invalid_title"},
		{ErrTextTooLong, "caption_too_long"},
		{ErrInvalidCategory, "invalid_category"},
		{ErrTooManyHashtags, "invalid_hashtags"},
		{ErrChannelRequired, "channel_required"},
		{ErrMediaNotOwned, "media_unavailable"},
		{ErrInvalidContentType, "invalid_content_type"},
		{ErrInvalidTier, BlockReasonInvalidTier},
		{errors.New("content rejected: spam"), "content_rejected"},
	} {
		got, terminal := postDraftBlockReason(c.err)
		if !terminal || got != c.want {
			t.Errorf("%v: (%q, %v), want (%q, true)", c.err, got, terminal, c.want)
		}
	}
	for _, transient := range []error{nil, errors.New("dial tcp: refused"), context.DeadlineExceeded, ErrStandingUnknown, ErrMediaNotReady} {
		if reason, terminal := postDraftBlockReason(transient); terminal {
			t.Errorf("%v parked as %q; a transient failure must back off", transient, reason)
		}
	}
}

// fakePostDraftQueue is the composer queue's store slice.
type fakePostDraftQueue struct {
	mu       sync.Mutex
	due      []postgres.PostDraft
	deferred map[uuid.UUID]*uuid.UUID // draft id -> claim token passed
	deferCtx []error                  // ctx.Err() seen by each defer
	plans    []postgres.PostDraftRetryPlan
}

func (f *fakePostDraftQueue) ClaimDuePostDrafts(context.Context, time.Time, time.Duration, int) ([]postgres.PostDraft, error) {
	return f.due, nil
}

func (f *fakePostDraftQueue) DeferPostDraftPublish(ctx context.Context, id uuid.UUID, token *uuid.UUID, plan postgres.PostDraftRetryPlan) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deferred == nil {
		f.deferred = map[uuid.UUID]*uuid.UUID{}
	}
	f.deferred[id] = token
	f.deferCtx = append(f.deferCtx, ctx.Err())
	f.plans = append(f.plans, plan)
	return false, nil
}

func TestScheduledPostDraftFailureBacksOffInsteadOfReleasing(t *testing.T) {
	ok, bad := uuid.New(), uuid.New()
	tok := uuid.New()
	q := &fakePostDraftQueue{due: []postgres.PostDraft{{ID: bad, ClaimToken: &tok}, {ID: ok}}}
	s := &Service{}
	s.postDraftQueueSeam = q
	s.publishPostDraftSeam = func(_ context.Context, d *postgres.PostDraft) error {
		if d.ID == bad {
			return errors.New("monetization unreachable")
		}
		return nil
	}
	n, err := s.PublishScheduledPostDrafts(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("published %d, err %v; want 1, nil", n, err)
	}
	got, deferred := q.deferred[bad]
	if !deferred || got == nil || *got != tok {
		t.Fatalf("the failed draft was not deferred under its claim token (deferred=%v token=%v)", deferred, got)
	}
	if _, wrong := q.deferred[ok]; wrong {
		t.Fatal("a published draft was deferred")
	}
	// The plan is the reel queue's: a minute, doubling, parked at the cap.
	retryAt, reason := q.plans[0](1)
	if reason != "" || time.Until(retryAt) < 50*time.Second || time.Until(retryAt) > 70*time.Second {
		t.Fatalf("first retry at %v (%q); want about a minute from now", retryAt, reason)
	}
	if _, reason := q.plans[0](maxReelDraftPublishAttempts); reason != BlockReasonPublishFailed {
		t.Fatalf("attempt %d: reason %q, want %q", maxReelDraftPublishAttempts, reason, BlockReasonPublishFailed)
	}
}

// M10: one hung draft costs its own deadline, not the tick, and is still
// deferred on a live context afterwards.
func TestScheduledPostDraftPublishHasItsOwnDeadline(t *testing.T) {
	hung, next := uuid.New(), uuid.New()
	q := &fakePostDraftQueue{due: []postgres.PostDraft{{ID: hung}, {ID: next}}}
	s := &Service{}
	s.postDraftQueueSeam = q
	s.draftPublishTimeout = 50 * time.Millisecond
	s.publishPostDraftSeam = func(ctx context.Context, d *postgres.PostDraft) error {
		if d.ID == hung {
			<-ctx.Done() // a dependency that never answers
			return ctx.Err()
		}
		return nil
	}
	start := time.Now()
	n, err := s.PublishScheduledPostDrafts(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("published %d, err %v; want the second draft published", n, err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("the tick took %v behind one hung draft", took)
	}
	if _, ok := q.deferred[hung]; !ok {
		t.Fatal("the timed-out draft was not deferred")
	}
	if q.deferCtx[0] != nil {
		t.Fatalf("the defer ran on a dead context (%v): the draft would stay 'publishing'", q.deferCtx[0])
	}
}

type fakeReelDraftQueue struct {
	due      []postgres.ReelDraft
	deferred []uuid.UUID
	deferCtx []error
}

func (f *fakeReelDraftQueue) ClaimDueReelDrafts(context.Context, time.Time, time.Duration, int) ([]postgres.ReelDraft, error) {
	return f.due, nil
}

func (f *fakeReelDraftQueue) DeferReelDraftPublish(ctx context.Context, id uuid.UUID, _ postgres.ReelDraftRetryPlan) (bool, error) {
	f.deferred = append(f.deferred, id)
	f.deferCtx = append(f.deferCtx, ctx.Err())
	return false, nil
}

func TestScheduledReelDraftPublishHasItsOwnDeadline(t *testing.T) {
	hung, next := uuid.New(), uuid.New()
	q := &fakeReelDraftQueue{due: []postgres.ReelDraft{{ID: hung}, {ID: next}}}
	s := &Service{}
	s.reelDraftQueueSeam = q
	s.draftPublishTimeout = 50 * time.Millisecond
	s.publishReelDraftSeam = func(ctx context.Context, id, _ uuid.UUID) error {
		if id == hung {
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}
	start := time.Now()
	n, err := s.PublishScheduledDrafts(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("published %d, err %v; want the second reel published", n, err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("the tick took %v behind one hung reel", took)
	}
	if len(q.deferred) != 1 || q.deferred[0] != hung || q.deferCtx[0] != nil {
		t.Fatalf("deferred %v (ctx errs %v); want the hung reel deferred on a live context", q.deferred, q.deferCtx)
	}
}

func TestScheduledDraftDeadlineIsAMinute(t *testing.T) {
	if got := (&Service{}).scheduledDraftTimeout(); got != time.Minute {
		t.Fatalf("default per-draft deadline %v, want 1m", got)
	}
}

// ── H2 / M8: a series on a video is its author's own; one batched read ────

func TestAddEpisodeRefusesSomeoneElsesVideo(t *testing.T) {
	owner, victim, id, post := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	store := seriesFixture(id, owner, true)
	store.postAuthors = map[uuid.UUID]uuid.UUID{post: victim}
	_, err := newSeriesService(store).AddEpisodeToVideoSeries(context.Background(), owner, id, post, 1, nil)
	if !errors.Is(err, ErrEpisodeNotCreatorsVideo) {
		t.Fatalf("adding a stranger's video: err=%v want ErrEpisodeNotCreatorsVideo", err)
	}
	if store.addCalls != 0 {
		t.Fatal("the stranger's video reached the store")
	}
	// The creator's own video is still added.
	own := uuid.New()
	store.postAuthors[own] = owner
	if _, err := newSeriesService(store).AddEpisodeToVideoSeries(context.Background(), owner, id, own, 1, nil); err != nil {
		t.Fatalf("adding the creator's own video: %v", err)
	}
}

func TestGetPostSeriesNeverShowsAForeignSeries(t *testing.T) {
	author, attacker, post := uuid.New(), uuid.New(), uuid.New()
	own, foreign := uuid.New(), uuid.New()
	store := &fakeVideoSeriesStore{series: map[uuid.UUID]*postgres.VideoSeries{
		own:     {ID: own, CreatorID: author, Title: "mine", IsPublic: true},
		foreign: {ID: foreign, CreatorID: attacker, Title: "scam", IsPublic: true},
	}}
	store.postAuthors = map[uuid.UUID]uuid.UUID{post: author}
	// The attacker's row is the newest, which used to win.
	store.memberships = []postgres.VideoSeriesEpisode{
		{SeriesID: foreign, PostID: post, EpisodeNum: 1},
		{SeriesID: own, PostID: post, EpisodeNum: 4},
	}
	store.episodes = []postgres.VideoSeriesEpisode{{SeriesID: own, PostID: post, EpisodeNum: 4}}
	view, err := newSeriesService(store).GetPostSeries(context.Background(), post, nil)
	if err != nil {
		t.Fatalf("unexpected err %v", err)
	}
	if view.Series.ID != own {
		t.Fatalf("the watch page shows %q; want the author's own series", view.Series.Title)
	}
	// Only the foreign series: no series at all.
	store.memberships = store.memberships[:1]
	if _, err := newSeriesService(store).GetPostSeries(context.Background(), post, nil); !errors.Is(err, ErrPostNotInSeries) {
		t.Fatalf("a video in a stranger's series only: err=%v want ErrPostNotInSeries", err)
	}
}

func TestEpisodeListsUseOneBatchedReadAndDropForeignVideos(t *testing.T) {
	owner, stranger, id := uuid.New(), uuid.New(), uuid.New()
	mine1, mine2, theirs, hidden := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	store := seriesFixture(id, owner, true)
	store.episodes = []postgres.VideoSeriesEpisode{
		{SeriesID: id, PostID: mine1, EpisodeNum: 1}, {SeriesID: id, PostID: theirs, EpisodeNum: 2},
		{SeriesID: id, PostID: hidden, EpisodeNum: 3}, {SeriesID: id, PostID: mine2, EpisodeNum: 4},
	}
	s := &Service{}
	s.videoSeries = store
	calls := 0
	var asked []uuid.UUID
	s.viewablePostsSeam = func(_ context.Context, ids []uuid.UUID, _ *uuid.UUID) (map[uuid.UUID]uuid.UUID, error) {
		calls++
		asked = ids
		return map[uuid.UUID]uuid.UUID{mine1: owner, mine2: owner, theirs: stranger}, nil
	}
	eps, err := s.GetVideoSeriesEpisodes(context.Background(), id, &stranger)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(asked) != 4 {
		t.Fatalf("%d visibility reads for %d ids; want one batched read of all 4", calls, len(asked))
	}
	if len(eps) != 2 || eps[0].PostID != mine1 || eps[1].PostID != mine2 {
		t.Fatalf("episodes %+v; want the creator's own two visible videos", eps)
	}
	// A failed batch read lists nothing (fail closed).
	s.viewablePostsSeam = func(context.Context, []uuid.UUID, *uuid.UUID) (map[uuid.UUID]uuid.UUID, error) {
		return nil, errors.New("db down")
	}
	if eps, _ := s.GetVideoSeriesEpisodes(context.Background(), id, &stranger); len(eps) != 0 {
		t.Fatalf("a failed read listed %d episodes", len(eps))
	}
}

// ── L1: series and playlists respect blocks and private accounts ───────────

func TestSeriesAndPlaylistMetadataRespectTheAuthorGate(t *testing.T) {
	owner, viewer, id := uuid.New(), uuid.New(), uuid.New()
	ss := newSeriesService(seriesFixture(id, owner, true))
	ss.hiddenAuthors = fakeHidden{owner: true}
	if _, err := ss.GetVideoSeries(context.Background(), id, &viewer); !errors.Is(err, ErrVideoSeriesNotFound) {
		t.Fatalf("series of an author the viewer may not view: err=%v want ErrVideoSeriesNotFound", err)
	}
	if list, err := ss.ListVideoSeriesByCreator(context.Background(), owner, &viewer, 10, 0); err != nil || len(list) != 0 {
		t.Fatalf("listing: %d series, err %v; want none", len(list), err)
	}
	if _, err := ss.GetVideoSeries(context.Background(), id, &owner); err != nil {
		t.Fatalf("the owner reads their own series: %v", err)
	}

	pl := &postgres.Playlist{ID: uuid.New(), CreatorID: owner, Title: "t", Visibility: "public"}
	store := &fakeAuthoringStore{playlist: pl, listed: []postgres.Playlist{*pl}}
	ps := newAuthoringService(store)
	ps.hiddenAuthors = fakeHidden{owner: true}
	if _, err := ps.GetPlaylist(context.Background(), pl.ID, &viewer); !errors.Is(err, ErrPlaylistNotFound) {
		t.Fatalf("playlist of an author the viewer may not view: err=%v want ErrPlaylistNotFound", err)
	}
	if _, err := ps.GetPlaylistItems(context.Background(), pl.ID, &viewer); !errors.Is(err, ErrPlaylistNotFound) {
		t.Fatalf("its items: err=%v want ErrPlaylistNotFound", err)
	}
	if list, err := ps.ListPlaylistsByCreator(context.Background(), owner, &viewer, 10, 0); err != nil || len(list) != 0 {
		t.Fatalf("listing: %d playlists, err %v; want none", len(list), err)
	}
	if _, err := ps.GetPlaylist(context.Background(), pl.ID, &owner); err != nil {
		t.Fatalf("the owner reads their own playlist: %v", err)
	}
}

// ── M3 / L4 / L2: covers and thumbnails are the owner's own platform image ─

type fakeOwnMedia map[uuid.UUID]postgres.MediaOwnership

func (f fakeOwnMedia) BatchGetMediaOwnership(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]postgres.MediaOwnership, error) {
	out := map[uuid.UUID]postgres.MediaOwnership{}
	for _, id := range ids {
		if m, ok := f[id]; ok {
			out[id] = m
		}
	}
	return out, nil
}

func tubeOwnImage(owner uuid.UUID) postgres.MediaOwnership {
	return postgres.MediaOwnership{UploaderID: owner, Kind: "image", ProcessingStatus: "ready", ModerationStatus: "passed"}
}

func TestSeriesCoverMustBeTheCreatorsOwnImage(t *testing.T) {
	owner, other, id := uuid.New(), uuid.New(), uuid.New()
	mine, theirs, pending := uuid.New(), uuid.New(), uuid.New()
	media := fakeOwnMedia{mine: tubeOwnImage(owner), theirs: tubeOwnImage(other),
		pending: {UploaderID: owner, Kind: "image", ProcessingStatus: "pending_upload", ModerationStatus: "pending"}}
	store := seriesFixture(id, owner, true)
	s := newSeriesService(store)
	s.ownMedia = media
	if _, err := s.UpdateVideoSeries(context.Background(), owner, id, postgres.VideoSeriesPatch{CoverMediaID: &theirs}); !errors.Is(err, ErrMediaNotOwned) {
		t.Fatalf("a stranger's media as the cover: err=%v want ErrMediaNotOwned", err)
	}
	if _, err := s.UpdateVideoSeries(context.Background(), owner, id, postgres.VideoSeriesPatch{CoverMediaID: &pending}); !errors.Is(err, ErrMediaNotReady) {
		t.Fatalf("an unfinished upload as the cover: err=%v want ErrMediaNotReady", err)
	}
	if len(store.patches) != 0 {
		t.Fatal("a refused cover reached the store")
	}
	if _, err := s.UpdateVideoSeries(context.Background(), owner, id, postgres.VideoSeriesPatch{CoverMediaID: &mine}); err != nil {
		t.Fatalf("the creator's own image: %v", err)
	}
	// Create runs the same check before the store (pgStore is nil here: a
	// cover that slipped through would panic rather than pass).
	err := s.CreateVideoSeries(context.Background(), &postgres.VideoSeries{CreatorID: owner, Title: "x", CoverMediaID: &theirs})
	var cover *CoverMediaError
	if !errors.As(err, &cover) || !errors.Is(err, ErrMediaNotOwned) {
		t.Fatalf("create with a stranger's cover: err=%v want a CoverMediaError(ErrMediaNotOwned)", err)
	}
}

func TestParsePlatformMediaRef(t *testing.T) {
	id := uuid.New()
	for _, ok := range []string{id.String(), " " + id.String() + " ", "/v1/media/" + id.String() + "/serve", "/v1/media/" + id.String() + "/serve/thumb"} {
		if got, good := parsePlatformMediaRef(ok); !good || got != id {
			t.Errorf("%q: (%v, %v), want the id", ok, got, good)
		}
	}
	for _, bad := range []string{"", "https://tracker.example/p.gif", "https://cdn.example/v1/media/" + id.String() + "/serve",
		"/v1/media/" + id.String(), "/v1/media/" + id.String() + "/serve?u=1", uuid.Nil.String(), "{" + id.String() + "}"} {
		if _, good := parsePlatformMediaRef(bad); good {
			t.Errorf("%q accepted as platform media", bad)
		}
	}
}

func TestPlaylistCoverMustBePlatformMediaOfTheOwner(t *testing.T) {
	owner, other := uuid.New(), uuid.New()
	mine, theirs := uuid.New(), uuid.New()
	s := &Service{}
	s.ownMedia = fakeOwnMedia{mine: tubeOwnImage(owner), theirs: tubeOwnImage(other)}
	ctx := context.Background()
	if _, err := s.normalizePlaylistCover(ctx, owner, strptr("https://tracker.example/pixel.gif")); !errors.Is(err, ErrInvalidPlaylistCover) {
		t.Fatalf("an external URL: err=%v want ErrInvalidPlaylistCover", err)
	}
	if _, err := s.normalizePlaylistCover(ctx, owner, strptr(theirs.String())); !errors.Is(err, ErrMediaNotOwned) {
		t.Fatalf("a stranger's image: err=%v want ErrMediaNotOwned", err)
	}
	got, err := s.normalizePlaylistCover(ctx, owner, strptr(mine.String()))
	if err != nil || got == nil || *got != "/v1/media/"+mine.String()+"/serve" {
		t.Fatalf("own image: %v, %v; want its serve path", got, err)
	}
	if got, err := s.normalizePlaylistCover(ctx, owner, nil); err != nil || got != nil {
		t.Fatalf("absent cover: %v, %v", got, err)
	}
	// Create refuses before the store (pgStore is nil).
	err = s.CreatePlaylist(ctx, &postgres.Playlist{CreatorID: owner, Title: "x", CoverURL: strptr("https://evil.example/x.png")})
	if !errors.Is(err, ErrInvalidPlaylistCover) {
		t.Fatalf("create with an external cover: err=%v", err)
	}
}

func TestValidateChapters(t *testing.T) {
	ch := func(idx, start int, title string) postgres.MediaChapter {
		return postgres.MediaChapter{ChapterIndex: idx, StartMs: start, Title: title}
	}
	ok := []postgres.MediaChapter{ch(1, 60000, "b"), ch(0, 0, "a"), ch(2, 120000, "c")}
	if err := validateChapters(ok, 600000); err != nil {
		t.Fatalf("a valid list (out of index order on the wire): %v", err)
	}
	if err := validateChapters(ok, 0); err != nil {
		t.Fatalf("unknown duration: %v", err)
	}
	many := make([]postgres.MediaChapter, MaxChapters+1)
	for i := range many {
		many[i] = ch(i, i*1000, "x")
	}
	for _, c := range []struct {
		name string
		in   []postgres.MediaChapter
		dur  int
		want error
	}{
		{"too many", many, 0, ErrInvalidChapters},
		{"duplicate index", []postgres.MediaChapter{ch(0, 0, "a"), ch(0, 5000, "b")}, 0, ErrDuplicateChapterIndex},
		{"negative index", []postgres.MediaChapter{ch(-1, 0, "a")}, 0, ErrInvalidChapters},
		{"empty title", []postgres.MediaChapter{ch(0, 0, "  ")}, 0, ErrInvalidChapters},
		{"title too long", []postgres.MediaChapter{ch(0, 0, strings.Repeat("é", MaxChapterTitleRunes+1))}, 0, ErrInvalidChapters},
		{"negative start", []postgres.MediaChapter{ch(0, -5, "a")}, 0, ErrInvalidChapters},
		{"not ascending", []postgres.MediaChapter{ch(0, 0, "a"), ch(1, 9000, "b"), ch(2, 9000, "c")}, 0, ErrInvalidChapters},
		{"backwards", []postgres.MediaChapter{ch(0, 5000, "a"), ch(1, 1000, "b")}, 0, ErrInvalidChapters},
		{"past the end", []postgres.MediaChapter{ch(0, 0, "a"), ch(1, 600000, "b")}, 600000, ErrInvalidChapters},
	} {
		if err := validateChapters(c.in, c.dur); !errors.Is(err, c.want) {
			t.Errorf("%s: err=%v want %v", c.name, err, c.want)
		}
	}
	if err := validateChapters([]postgres.MediaChapter{ch(0, 0, strings.Repeat("é", MaxChapterTitleRunes))}, 0); err != nil {
		t.Fatalf("a title at the limit: %v", err)
	}
}

type fakeChapterStore struct {
	saved   []postgres.MediaChapter
	saves   int
	saveErr error
	stored  []postgres.MediaChapter
}

func (f *fakeChapterStore) SaveChapters(_ context.Context, _ uuid.UUID, chapters []postgres.MediaChapter) error {
	f.saves++
	f.saved = chapters
	return f.saveErr
}

func (f *fakeChapterStore) GetChapters(context.Context, uuid.UUID) ([]postgres.MediaChapter, error) {
	return f.stored, nil
}

func TestSaveChaptersValidatesBeforeWriting(t *testing.T) {
	owner, other := uuid.New(), uuid.New()
	mine, theirs := uuid.New(), uuid.New()
	newSvc := func() (*Service, *fakeChapterStore) {
		st := &fakeChapterStore{}
		s := newAuthoringService(&fakeAuthoringStore{authorID: owner})
		s.chapterStoreSeam = st
		s.ownMedia = fakeOwnMedia{mine: tubeOwnImage(owner), theirs: tubeOwnImage(other)}
		return s, st
	}
	ctx, post := context.Background(), uuid.New()

	s, st := newSvc()
	err := s.SaveChapters(ctx, owner, post, []postgres.MediaChapter{{ChapterIndex: 0, Title: "a"}, {ChapterIndex: 0, Title: "b", StartMs: 9}})
	if !errors.Is(err, ErrDuplicateChapterIndex) || st.saves != 0 {
		t.Fatalf("duplicate index: err=%v saves=%d; want ErrDuplicateChapterIndex before any write", err, st.saves)
	}
	for _, thumb := range []string{"https://tracker.example/t.gif", theirs.String()} {
		s, st = newSvc()
		th := thumb
		err = s.SaveChapters(ctx, owner, post, []postgres.MediaChapter{{ChapterIndex: 0, Title: "a", ThumbnailURL: &th}})
		if err == nil || st.saves != 0 {
			t.Fatalf("thumbnail %q accepted (saves=%d)", thumb, st.saves)
		}
	}
	s, st = newSvc()
	th := mine.String()
	if err := s.SaveChapters(ctx, owner, post, []postgres.MediaChapter{{ChapterIndex: 0, Title: "a", ThumbnailURL: &th}, {ChapterIndex: 1, Title: "b", StartMs: 10}}); err != nil {
		t.Fatalf("a valid list: %v", err)
	}
	if st.saved[0].ThumbnailURL == nil || *st.saved[0].ThumbnailURL != "/v1/media/"+mine.String()+"/serve" || st.saved[1].ThumbnailURL != nil {
		t.Fatalf("thumbnails stored as %v / %v; want the serve path and none", st.saved[0].ThumbnailURL, st.saved[1].ThumbnailURL)
	}
	// The primary key, should anything reach it, is a 400 too, not a 500.
	s, st = newSvc()
	st.saveErr = &pgconn.PgError{Code: "23505", Message: `duplicate key value violates unique constraint "media_chapters_pkey"`}
	if err := s.SaveChapters(ctx, owner, post, []postgres.MediaChapter{{ChapterIndex: 0, Title: "a"}}); !errors.Is(err, ErrDuplicateChapterIndex) {
		t.Fatalf("a unique violation from the store: err=%v want ErrDuplicateChapterIndex", err)
	}
}

// ── M2: the chapters route applies the members-only gate ───────────────────

func TestChaptersOfAMembersOnlyVideoAreTheMembers(t *testing.T) {
	author, tier := uuid.New(), uuid.New()
	st := &fakeChapterStore{stored: []postgres.MediaChapter{{ChapterIndex: 0, Title: "secret plot point"}}}
	s := &Service{}
	s.chapterStoreSeam = st
	s.postGateSeam = func(context.Context, uuid.UUID) (uuid.UUID, *uuid.UUID, error) { return author, &tier, nil }
	got, err := s.GetChaptersForViewer(context.Background(), uuid.New(), nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("a signed-out viewer of a members-only video got %d chapters (err %v); want none", len(got), err)
	}
	if got, err := s.GetChaptersForViewer(context.Background(), uuid.New(), &author); err != nil || len(got) != 1 {
		t.Fatalf("the author got %d chapters (err %v); want theirs", len(got), err)
	}
	// An open video's chapters are everyone's.
	s.postGateSeam = func(context.Context, uuid.UUID) (uuid.UUID, *uuid.UUID, error) { return author, nil, nil }
	if got, err := s.GetChaptersForViewer(context.Background(), uuid.New(), nil); err != nil || len(got) != 1 {
		t.Fatalf("an open video: %d chapters (err %v)", len(got), err)
	}
	// A gate lookup that fails is an error, never the chapters.
	s.postGateSeam = func(context.Context, uuid.UUID) (uuid.UUID, *uuid.UUID, error) {
		return uuid.Nil, nil, errors.New("db down")
	}
	if got, err := s.GetChaptersForViewer(context.Background(), uuid.New(), nil); err == nil || got != nil {
		t.Fatalf("a failed gate lookup served %d chapters", len(got))
	}
}

// ── M1 / M4: playlist adds and pages ───────────────────────────────────────

type fakePlaylistItems struct {
	gotPosition *int
	gotMax      int
	calls       int
	err         error
}

func (f *fakePlaylistItems) AddPlaylistItem(_ context.Context, playlistID, postID uuid.UUID, position *int, maxItems int) (*postgres.PlaylistItem, error) {
	f.calls++
	f.gotPosition, f.gotMax = position, maxItems
	if f.err != nil {
		return nil, f.err
	}
	return &postgres.PlaylistItem{PlaylistID: playlistID, PostID: postID, Position: 7}, nil
}

func TestAddPlaylistItemAppendsAndRefusesDuplicatesAndOverflow(t *testing.T) {
	owner := uuid.New()
	pl := &postgres.Playlist{ID: uuid.New(), CreatorID: owner, Visibility: "public", Kind: postgres.PlaylistKindUser}
	items := &fakePlaylistItems{}
	s := newAuthoringService(&fakeAuthoringStore{playlist: pl})
	s.playlistItemsSeam = items
	ctx := context.Background()

	item, err := s.AddPlaylistItem(ctx, owner, pl.ID, uuid.New(), nil)
	if err != nil || item.Position != 7 {
		t.Fatalf("append: %+v, %v", item, err)
	}
	if items.gotPosition != nil || items.gotMax != postgres.MaxPlaylistItems {
		t.Fatalf("the store got position %v and cap %d; want nil (append) and %d", items.gotPosition, items.gotMax, postgres.MaxPlaylistItems)
	}
	neg := -1
	if _, err := s.AddPlaylistItem(ctx, owner, pl.ID, uuid.New(), &neg); !errors.Is(err, ErrPlaylistPositionInvalid) || items.calls != 1 {
		t.Fatalf("negative position: err=%v store calls=%d", err, items.calls)
	}
	items.err = postgres.ErrPlaylistItemExists
	if _, err := s.AddPlaylistItem(ctx, owner, pl.ID, uuid.New(), nil); !errors.Is(err, ErrPlaylistItemExists) {
		t.Fatalf("duplicate: err=%v", err)
	}
	items.err = postgres.ErrPlaylistFull
	if _, err := s.AddPlaylistItem(ctx, owner, pl.ID, uuid.New(), nil); !errors.Is(err, ErrPlaylistFull) {
		t.Fatalf("full: err=%v", err)
	}
}

func TestPlaylistItemsArePaged(t *testing.T) {
	if postgres.MaxPlaylistItems != 5000 {
		t.Fatalf("MaxPlaylistItems = %d; the cap is 5000", postgres.MaxPlaylistItems)
	}
	for _, c := range []struct{ in, want int }{{0, DefaultPlaylistItemsPage}, {-3, DefaultPlaylistItemsPage}, {20, 20}, {1000, postgres.MaxPlaylistItemsPage}} {
		if got := clampPlaylistPage(c.in); got != c.want {
			t.Errorf("clamp(%d) = %d, want %d", c.in, got, c.want)
		}
	}
	for _, bad := range []string{"x", "-1", "1.5"} {
		if _, err := ParsePlaylistItemsCursor(bad); !errors.Is(err, ErrInvalidPlaylistCursor) {
			t.Errorf("cursor %q: err=%v", bad, err)
		}
	}
	if after, err := ParsePlaylistItemsCursor(""); err != nil || after != nil {
		t.Fatalf("empty cursor: %v %v", after, err)
	}
	if after, err := ParsePlaylistItemsCursor("41"); err != nil || after == nil || *after != 41 {
		t.Fatalf("cursor 41: %v %v", after, err)
	}

	owner := uuid.New()
	pl := &postgres.Playlist{ID: uuid.New(), CreatorID: owner, Visibility: "public"}
	page := []postgres.PlaylistItem{{PostID: uuid.New(), Position: 3}, {PostID: uuid.New(), Position: 9}}
	store := &pagedAuthoring{fakeAuthoringStore: fakeAuthoringStore{playlist: pl, items: page}}
	s := newAuthoringService(store)
	hydrated := 0
	s.batchPostsSeam = func(_ context.Context, ids []uuid.UUID, _ *uuid.UUID) (map[uuid.UUID]*PostDetail, error) {
		hydrated = len(ids)
		return map[uuid.UUID]*PostDetail{}, nil
	}
	nine := 9
	out, next, err := s.GetPlaylistItemsHydrated(context.Background(), pl.ID, nil, postgres.PlaylistItemsPage{AfterPosition: &nine, Limit: 2})
	if err != nil || len(out) != 2 || next != "9" {
		t.Fatalf("a full page: %d rows, next %q, err %v; want 2 rows and cursor 9", len(out), next, err)
	}
	if store.lastPage.Limit != 2 || store.lastPage.AfterPosition == nil || *store.lastPage.AfterPosition != 9 || hydrated != 2 {
		t.Fatalf("store asked %+v, hydrated %d; want the page asked for, hydrated alone", store.lastPage, hydrated)
	}
	_, next, _ = s.GetPlaylistItemsHydrated(context.Background(), pl.ID, nil, postgres.PlaylistItemsPage{Limit: 10})
	if next != "" {
		t.Fatalf("a short page carries cursor %q; want none", next)
	}
	if store.lastPage.Limit != 10 {
		t.Fatalf("limit %d reached the store", store.lastPage.Limit)
	}
}

type pagedAuthoring struct {
	fakeAuthoringStore
	lastPage postgres.PlaylistItemsPage
}

func (p *pagedAuthoring) GetPlaylistItems(_ context.Context, _ uuid.UUID, page postgres.PlaylistItemsPage) ([]postgres.PlaylistItem, error) {
	p.lastPage = page
	return p.items, nil
}

// ── L3: watch progress ─────────────────────────────────────────────────────

type fakeProgressWriter struct{ applied bool }

func (f fakeProgressWriter) UpsertWatchProgress(_ context.Context, wp *postgres.WatchProgress) (bool, error) {
	wp.LastWatchedAt = time.Now()
	return f.applied, nil
}

func TestWatchProgressSaveSurvivesARedisFailure(t *testing.T) {
	// A Redis that is not there: the address refuses connections at once.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	rdb := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: -1, DialTimeout: 200 * time.Millisecond})
	defer rdb.Close()
	s := &Service{rdb: rdb}
	s.watchProgressSeam = fakeProgressWriter{applied: true}
	wp := &postgres.WatchProgress{UserID: uuid.New(), PostID: uuid.New(), PositionMs: 5000}
	if err := s.SaveWatchProgress(context.Background(), wp); err != nil {
		t.Fatalf("a save the database kept failed on the Redis mirror: %v", err)
	}
}

// ── M7: a page's view counts are one batched call under one deadline ───────

func TestPageViewCountsAreOneCallUnderOneDeadline(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	analytics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		select { // a hung analytics-service
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer analytics.Close()
	defer close(release)
	s := &Service{analyticsServiceURL: analytics.URL, httpClient: &http.Client{Timeout: 30 * time.Second}}
	rows := make([]*PostDetail, 40)
	for i := range rows {
		rows[i] = &PostDetail{Post: &postgres.Post{ID: uuid.New()}}
	}
	start := time.Now()
	s.attachPageViewCounts(context.Background(), rows)
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("40 rows behind a hung analytics took %v; one deadline is %v", took, viewCountPageDeadline)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("%d calls to analytics for one page; want one batched call", n)
	}

	// A page bigger than one analytics batch (200 ids) is several calls,
	// each with its own 1.5 s timeout, and still one deadline in total.
	big := make([]*PostDetail, 3*contentViewsBatchMax)
	for i := range big {
		big[i] = &PostDetail{Post: &postgres.Post{ID: uuid.New()}}
	}
	start = time.Now()
	s.attachPageViewCounts(context.Background(), big)
	if took := time.Since(start); took > viewCountPageDeadline+time.Second {
		t.Fatalf("a %d-row page behind a hung analytics took %v; the page deadline is %v", len(big), took, viewCountPageDeadline)
	}
}

// The two pages the audit named read their counts through the page helpers,
// never row by row (the per-row helpers stay for single-post reads).
func TestLiveRecordingsAndMyUploadsReadCountsPerPage(t *testing.T) {
	for file, fn := range map[string]string{
		"live_recordings.go": "func (s *Service) GetLiveRecordings(",
		"my_uploads.go":      "func (s *Service) enrichUploads(",
	} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		body := string(src)
		i := strings.Index(body, fn)
		if i < 0 {
			t.Fatalf("%s: %s not found", file, fn)
		}
		body = body[i:]
		if j := strings.Index(body[1:], "\nfunc "); j >= 0 {
			body = body[:j+1]
		}
		for _, perRow := range []string{"s.getViewCount(", "s.countsForPost("} {
			if strings.Contains(body, perRow) {
				t.Errorf("%s still calls %s per row", fn, perRow)
			}
		}
		if !strings.Contains(body, "s.pageCounts(") {
			t.Errorf("%s does not read the page's counts with pageCounts", fn)
		}
		if !strings.Contains(body, "s.pageViewCounts(") && !strings.Contains(body, "s.attachPageViewCounts(") {
			t.Errorf("%s does not read the page's view counts in one call", fn)
		}
	}
}

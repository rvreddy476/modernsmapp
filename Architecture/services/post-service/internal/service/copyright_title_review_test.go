package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	postEvents "github.com/atpost/post-service/internal/events"
	"github.com/atpost/post-service/internal/standing"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/post-service/internal/titlematch"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// Copyright title review (founder quick wins 2026-10-24). These drive the
// rule and its wiring without a database; the end-to-end path against real
// Postgres is copyright_title_review_integration_test.go (store) and
// copyright_title_review_service_integration_test.go (this package).

// fakeTitleStore is the review store: a catalogue, the calls made to it
// and canned answers.
type fakeTitleStore struct {
	titles      []postgres.CopyrightTitle
	listErr     error
	loads       int
	review      *postgres.TitleReview
	decided     []postgres.DecideTitleReviewInput
	announce    postgres.TitleReviewAnnounce
	resolved    []resolveCall
	resolveOut  postgres.TitleReviewDurationOutcome
	created     []postgres.CopyrightTitleWrite
	byPost      map[uuid.UUID]*postgres.TitleReview
	requestedBy []uuid.UUID
}

type resolveCall struct {
	post   uuid.UUID
	dur    int
	hold   bool
	reason string
}

func (f *fakeTitleStore) ListActiveCopyrightTitles(context.Context) ([]postgres.CopyrightTitle, error) {
	f.loads++
	return f.titles, f.listErr
}
func (f *fakeTitleStore) ListCopyrightTitles(context.Context, postgres.CopyrightTitleFilter) ([]postgres.CopyrightTitle, string, error) {
	return f.titles, "", nil
}
func (f *fakeTitleStore) GetCopyrightTitle(_ context.Context, id uuid.UUID) (*postgres.CopyrightTitle, error) {
	for i := range f.titles {
		if f.titles[i].ID == id {
			t := f.titles[i]
			return &t, nil
		}
	}
	return nil, postgres.ErrCopyrightTitleNotFound
}
func (f *fakeTitleStore) CreateCopyrightTitle(_ context.Context, id uuid.UUID, w postgres.CopyrightTitleWrite, actor uuid.UUID) (*postgres.CopyrightTitle, error) {
	f.created = append(f.created, w)
	t := postgres.CopyrightTitle{ID: id, Title: w.Title, Aliases: w.Aliases, Language: w.Language, OwnerChannelIDs: w.OwnerChannelIDs, Active: w.Active, CreatedBy: actor}
	f.titles = append(f.titles, t)
	return &t, nil
}
func (f *fakeTitleStore) UpdateCopyrightTitle(_ context.Context, id uuid.UUID, w postgres.CopyrightTitleWrite, _ uuid.UUID, _ string) (*postgres.CopyrightTitle, error) {
	for i := range f.titles {
		if f.titles[i].ID == id {
			f.titles[i].Title, f.titles[i].Aliases, f.titles[i].Active = w.Title, w.Aliases, w.Active
			t := f.titles[i]
			return &t, nil
		}
	}
	return nil, postgres.ErrCopyrightTitleNotFound
}
func (f *fakeTitleStore) ListTitleReviews(context.Context, string, string, int) ([]postgres.TitleReviewQueueItem, string, error) {
	return nil, "", nil
}
func (f *fakeTitleStore) GetTitleReview(context.Context, uuid.UUID) (*postgres.TitleReview, error) {
	if f.review == nil {
		return nil, postgres.ErrTitleReviewNotFound
	}
	return f.review, nil
}
func (f *fakeTitleStore) DecideTitleReview(_ context.Context, in postgres.DecideTitleReviewInput, announce postgres.TitleReviewAnnounce) (*postgres.TitleReviewOutcome, error) {
	f.decided = append(f.decided, in)
	f.announce = announce
	r := *f.review
	return &postgres.TitleReviewOutcome{Review: &r}, nil
}
func (f *fakeTitleStore) ResolveTitleReviewDuration(_ context.Context, postID uuid.UUID, dur int, hold bool, reason string, announce postgres.TitleReviewAnnounce) (postgres.TitleReviewDurationOutcome, error) {
	f.resolved = append(f.resolved, resolveCall{postID, dur, hold, reason})
	f.announce = announce
	return f.resolveOut, nil
}
func (f *fakeTitleStore) RequestTitleReview(_ context.Context, _ uuid.UUID, authorID uuid.UUID, _ string) (*postgres.TitleReview, error) {
	f.requestedBy = append(f.requestedBy, authorID)
	if f.review == nil {
		return nil, postgres.ErrTitleReviewNotFound
	}
	return f.review, nil
}
func (f *fakeTitleStore) TitleReviewsByPost(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]*postgres.TitleReview, error) {
	out := map[uuid.UUID]*postgres.TitleReview{}
	for _, id := range ids {
		if r, ok := f.byPost[id]; ok {
			out[id] = r
		}
	}
	return out, nil
}

// errChannels fails the author's channel lookup.
type errChannels struct {
	*fakeChannelStore
	err error
}

func (e errChannels) GetChannelByUserID(context.Context, uuid.UUID) (*postgres.Channel, error) {
	return nil, e.err
}

type titleRig struct {
	svc      *Service
	store    *fakeTitleStore
	channels *fakeChannelStore
	author   uuid.UUID
	jailer   uuid.UUID // "Jailer 2"
	leo      uuid.UUID // "Leo"
	ownerCh  uuid.UUID // a channel verified to own "Jailer 2"
}

func newTitleRig(t *testing.T) *titleRig {
	t.Helper()
	r := &titleRig{author: uuid.New(), jailer: uuid.New(), leo: uuid.New(), ownerCh: uuid.New()}
	r.store = &fakeTitleStore{titles: []postgres.CopyrightTitle{
		{ID: r.jailer, Title: "Jailer 2", Aliases: []string{"Jailer II"}, OwnerChannelIDs: []uuid.UUID{r.ownerCh}, Active: true},
		{ID: r.leo, Title: "Leo", Active: true},
		{ID: uuid.New(), Title: "Switched Off Film", Active: false},
	}}
	r.channels = newFakeChannelStore()
	r.svc = &Service{titleReviews: r.store, channels: r.channels, now: func() time.Time { return time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC) }}
	return r
}

func (r *titleRig) subject(contentType string, durationSec int, title string) titleHoldSubject {
	return titleHoldSubject{AuthorID: r.author, ContentType: contentType, DurationSec: durationSec, Title: title}
}

func TestTitleHold_DurationAndTypeGates(t *testing.T) {
	for _, c := range []struct {
		name        string
		contentType string
		duration    int
		hold        bool
		awaiting    bool
	}{
		{"long video over 20 min", "long_video", 20*60 + 1, true, false},
		{"legacy video kind over 20 min", "video", 2 * 60 * 60, true, false},
		{"exactly 20 min is not over", "long_video", 20 * 60, false, false},
		{"short long video", "long_video", 9 * 60, false, false},
		{"unmeasured long video holds, waiting", "long_video", 0, true, true},
		{"flick", "flick", 2 * 60 * 60, false, false},
		{"reel", "reel", 2 * 60 * 60, false, false},
		{"photo or text post", "post", 2 * 60 * 60, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newTitleRig(t)
			hold, err := r.svc.decideTitleHold(context.Background(), r.subject(c.contentType, c.duration, "Jailer 2 full movie HD"))
			if err != nil {
				t.Fatal(err)
			}
			if (hold != nil) != c.hold {
				t.Fatalf("hold = %+v, want held=%v", hold, c.hold)
			}
			if hold == nil {
				return
			}
			if hold.TitleID != r.jailer || hold.MatchedTitle != "Jailer 2" || hold.MatchedField != titlematch.FieldTitle || !hold.AnnounceOnClear {
				t.Fatalf("hold = %+v", hold)
			}
			if hold.AwaitingDuration != c.awaiting || (hold.DurationSeconds == nil) != c.awaiting {
				t.Fatalf("awaiting = %v duration = %v, want awaiting %v", hold.AwaitingDuration, hold.DurationSeconds, c.awaiting)
			}
		})
	}
}

func TestTitleHold_MatchesTextAndHashtags(t *testing.T) {
	r := newTitleRig(t)
	ctx := context.Background()
	sub := r.subject("long_video", 3000, "Weekend vlog")
	sub.Hashtags = []string{"#jailer2"}
	if hold, err := r.svc.decideTitleHold(ctx, sub); err != nil || hold == nil || hold.MatchedField != titlematch.FieldHashtag {
		t.Fatalf("hashtag: hold=%+v err=%v", hold, err)
	}
	sub = r.subject("long_video", 3000, "Weekend vlog")
	sub.Description = "full movie: jailer ii"
	if hold, err := r.svc.decideTitleHold(ctx, sub); err != nil || hold == nil || hold.MatchedTerm != "Jailer II" {
		t.Fatalf("description alias: hold=%+v err=%v", hold, err)
	}
	for _, title := range []string{"jailerbird", "Switched Off Film", "My trip to Leon"} {
		if hold, err := r.svc.decideTitleHold(ctx, r.subject("long_video", 3000, title)); err != nil || hold != nil {
			t.Fatalf("%q: hold=%+v err=%v, want none", title, hold, err)
		}
	}
}

func TestTitleHold_VerifiedOwnerIsNeverHeld(t *testing.T) {
	r := newTitleRig(t)
	ctx := context.Background()
	r.channels.byUser[r.author] = &postgres.Channel{ID: r.ownerCh, UserID: r.author}
	if hold, err := r.svc.decideTitleHold(ctx, r.subject("long_video", 3000, "Jailer 2 official")); err != nil || hold != nil {
		t.Fatalf("owner of Jailer 2: hold=%+v err=%v, want none", hold, err)
	}
	// Owning one title does not cover another the video also names.
	hold, err := r.svc.decideTitleHold(ctx, r.subject("long_video", 3000, "Jailer 2 vs Leo"))
	if err != nil || hold == nil || hold.TitleID != r.leo {
		t.Fatalf("owner of Jailer 2 naming Leo: hold=%+v err=%v, want held for Leo", hold, err)
	}
	// Another channel is not an owner.
	r.channels.byUser[r.author] = &postgres.Channel{ID: uuid.New(), UserID: r.author}
	if hold, _ := r.svc.decideTitleHold(ctx, r.subject("long_video", 3000, "Jailer 2 official")); hold == nil {
		t.Fatal("a non-owner channel was not held")
	}
}

func TestTitleHold_FailsClosedAndKillSwitch(t *testing.T) {
	r := newTitleRig(t)
	ctx := context.Background()
	r.store.listErr = errors.New("db down")
	if _, err := r.svc.decideTitleHold(ctx, r.subject("long_video", 3000, "Jailer 2")); err == nil {
		t.Fatal("an unreadable catalogue let the publication through")
	}
	r = newTitleRig(t)
	r.svc.channels = errChannels{fakeChannelStore: r.channels, err: errors.New("channels down")}
	if _, err := r.svc.decideTitleHold(ctx, r.subject("long_video", 3000, "Jailer 2")); err == nil {
		t.Fatal("an unreadable owner channel let the publication through")
	}
	r = newTitleRig(t)
	r.svc.SetCopyrightTitleHoldEnabled(false)
	if hold, err := r.svc.decideTitleHold(ctx, r.subject("long_video", 3000, "Jailer 2")); err != nil || hold != nil {
		t.Fatalf("kill switch: hold=%+v err=%v", hold, err)
	}
}

func TestTitleCatalogue_CachedAndDroppedOnWrite(t *testing.T) {
	r := newTitleRig(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := r.svc.decideTitleHold(ctx, r.subject("long_video", 3000, "Leo")); err != nil {
			t.Fatal(err)
		}
	}
	if r.store.loads != 1 {
		t.Fatalf("catalogue loaded %d times, want once inside the TTL", r.store.loads)
	}
	title := "Vikram"
	if _, err := r.svc.CreateCopyrightTitle(ctx, CopyrightTitleInput{Title: &title}, uuid.New()); err != nil {
		t.Fatal(err)
	}
	hold, err := r.svc.decideTitleHold(ctx, r.subject("long_video", 3000, "Vikram full movie"))
	if err != nil || hold == nil || r.store.loads != 2 {
		t.Fatalf("after a write: hold=%+v loads=%d err=%v, want the new title matched on a reload", hold, r.store.loads, err)
	}
}

func TestApplyCreateTitleHold_TierAndNoVideo(t *testing.T) {
	r := newTitleRig(t)
	ctx := context.Background()
	p := &postgres.Post{AuthorID: r.author, ContentType: "long_video", Title: "Jailer 2"}
	if err := r.svc.applyCreateTitleHold(ctx, p, 3000, false); err != nil || p.TitleReviewWrite != nil {
		t.Fatalf("no video attached: hold=%+v err=%v", p.TitleReviewWrite, err)
	}
	if err := r.svc.applyCreateTitleHold(ctx, p, 3000, true); err != nil || p.TitleReviewWrite == nil {
		t.Fatalf("held video: hold=%+v err=%v", p.TitleReviewWrite, err)
	}
	tier := uuid.New()
	gated := &postgres.Post{AuthorID: r.author, ContentType: "long_video", Title: "Jailer 2", TierRequiredID: &tier}
	if err := r.svc.applyCreateTitleHold(ctx, gated, 3000, true); !errors.Is(err, ErrCopyrightReviewTier) || gated.TitleReviewWrite != nil {
		t.Fatalf("members-only held video: err=%v hold=%+v, want ErrCopyrightReviewTier", err, gated.TitleReviewWrite)
	}
	open := &postgres.Post{AuthorID: r.author, ContentType: "long_video", Title: "Cooking", TierRequiredID: &tier}
	if err := r.svc.applyCreateTitleHold(ctx, open, 3000, true); err != nil {
		t.Fatalf("members-only unmatched video refused: %v", err)
	}
}

func TestCreatedEventWithheldForHeldAndScheduledPosts(t *testing.T) {
	svc := &Service{producer: &postEvents.Producer{}}
	ctx := context.Background()
	live := &postgres.Post{ID: uuid.New(), AuthorID: uuid.New(), ContentType: "long_video", ReviewStatus: "approved"}
	if typ, payload := svc.createdEventFor(ctx, live, nil, 3000, 1080); typ != events.PostCreated || payload == nil {
		t.Fatalf("live post: %q %v, want PostCreated", typ, payload)
	}
	held := *live
	held.TitleReviewWrite = &postgres.TitleReviewHold{ReviewID: uuid.New()}
	if typ, payload := svc.createdEventFor(ctx, &held, nil, 3000, 1080); typ != "" || payload != nil {
		t.Fatalf("held post announced itself: %q", typ)
	}
	at := time.Now().Add(time.Hour)
	scheduled := *live
	scheduled.PublishAt = &at
	if typ, _ := svc.createdEventFor(ctx, &scheduled, nil, 3000, 1080); typ != "" {
		t.Fatalf("scheduled post announced itself: %q", typ)
	}
}

func TestTitleReviewAnnounce_EligibleFromTheClear(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	svc := &Service{producer: &postEvents.Producer{}}
	post := &postgres.Post{ID: uuid.New(), AuthorID: uuid.New(), ContentType: "long_video", ReviewStatus: "approved",
		ActiveRestrictionCount: 1, CreatedAt: now.Add(-48 * time.Hour), Title: "Jailer 2"}
	typ, payload, err := svc.titleReviewAnnounce(context.Background(), post, now)(7)
	if err != nil || typ != events.PostCreated {
		t.Fatalf("type=%q err=%v", typ, err)
	}
	pc := payload.(events.PostCreatedPayload)
	if pc.EligibleFrom == nil || !pc.EligibleFrom.Equal(now) {
		t.Fatalf("eligible_from = %v, want the clear time %v", pc.EligibleFrom, now)
	}
	if pc.SearchRev != 7 || pc.ReviewStatus != "approved" || !pc.CreatedAt.Equal(post.CreatedAt) {
		t.Fatalf("payload rev=%d review=%q created=%v", pc.SearchRev, pc.ReviewStatus, pc.CreatedAt)
	}
	if post.ActiveRestrictionCount != 1 {
		t.Fatal("the announce mutated the caller's post")
	}
	noProducer := &Service{}
	if typ, _, _ := noProducer.titleReviewAnnounce(context.Background(), post, now)(7); typ != "" {
		t.Fatalf("no producer still announced %q", typ)
	}
}

func TestDecideCopyrightReview_ValidatesBeforeTheStore(t *testing.T) {
	r := newTitleRig(t)
	ctx := context.Background()
	r.store.review = &postgres.TitleReview{ID: uuid.New(), PostID: uuid.New(), State: postgres.TitleReviewPending}
	if _, err := r.svc.DecideCopyrightReview(ctx, r.store.review.ID, uuid.New(), "approve", ""); !errors.Is(err, postgres.ErrTitleReviewInvalid) {
		t.Fatalf("unknown decision: err=%v", err)
	}
	if _, err := r.svc.DecideCopyrightReview(ctx, r.store.review.ID, uuid.New(), "block", strings.Repeat("x", 2001)); !errors.Is(err, postgres.ErrTitleReviewInvalid) {
		t.Fatalf("long note: err=%v", err)
	}
	if len(r.store.decided) != 0 {
		t.Fatal("an invalid decision reached the store")
	}
	actor := uuid.New()
	if _, err := r.svc.DecideCopyrightReview(ctx, r.store.review.ID, actor, " BLOCK ", "pirated upload"); err != nil {
		t.Fatal(err)
	}
	if len(r.store.decided) != 1 || r.store.decided[0].Decision != postgres.TitleReviewDecisionBlock || r.store.decided[0].ActorID != actor {
		t.Fatalf("decided = %+v", r.store.decided)
	}
}

func TestResolveTitleReviewMeasurement_QueuesOrLifts(t *testing.T) {
	post := uuid.New()
	for _, c := range []struct {
		name     string
		kind     string
		duration int
		calls    int
		hold     bool
		reason   string
	}{
		{"long and over", "long_video", 3600, 1, true, ""},
		{"long but short", "long_video", 600, 1, false, ""},
		{"exactly the threshold", "long_video", 1200, 1, false, ""},
		{"reclassified to a flick", "flick", 3600, 1, false, postgres.TitleReviewReasonNotLongVideo},
		{"not measured", "long_video", 0, 0, false, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newTitleRig(t)
			if err := r.svc.ResolveTitleReviewMeasurement(context.Background(), post, c.duration, c.kind); err != nil {
				t.Fatal(err)
			}
			if len(r.store.resolved) != c.calls {
				t.Fatalf("resolve calls = %d, want %d", len(r.store.resolved), c.calls)
			}
			if c.calls == 0 {
				return
			}
			got := r.store.resolved[0]
			if got.post != post || got.dur != c.duration || got.hold != c.hold || got.reason != c.reason {
				t.Fatalf("resolve = %+v, want hold=%v reason=%q", got, c.hold, c.reason)
			}
		})
	}
}

func TestCopyrightReviewView_AuthorOnlyAndNeverFlaggedIsAbsent(t *testing.T) {
	r := newTitleRig(t)
	ctx := context.Background()
	mine, theirs, quiet := uuid.New(), uuid.New(), uuid.New()
	decided := time.Now()
	r.store.byPost = map[uuid.UUID]*postgres.TitleReview{
		mine:   {PostID: mine, State: postgres.TitleReviewBlocked, MatchedTitle: "Jailer 2", AuthorNotified: true, DecidedAt: &decided},
		theirs: {PostID: theirs, State: postgres.TitleReviewPending, MatchedTitle: "Leo", AuthorNotified: true},
		quiet:  {PostID: quiet, State: postgres.TitleReviewCleared, MatchedTitle: "Leo", AuthorNotified: false},
	}
	other := uuid.New()
	details := []*PostDetail{
		{Post: &postgres.Post{ID: mine, AuthorID: r.author, ContentType: "long_video"}},
		{Post: &postgres.Post{ID: theirs, AuthorID: other, ContentType: "long_video"}},
		{Post: &postgres.Post{ID: quiet, AuthorID: r.author, ContentType: "long_video"}},
	}
	r.svc.attachCopyrightReviews(ctx, r.author, details)
	if v := details[0].CopyrightReview; v == nil || v.State != "blocked" || v.MatchedTitle != "Jailer 2" || v.DecidedAt == nil {
		t.Fatalf("owner's blocked video: %+v", v)
	}
	if details[1].CopyrightReview != nil {
		t.Fatal("another author's review was shown")
	}
	if details[2].CopyrightReview != nil {
		t.Fatal("a hold lifted before the author was told is shown")
	}
}

func TestRequireLongVideoUploads_PauseOnlyLongVideo(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	until := now.Add(72 * time.Hour)
	svc := &Service{now: func() time.Time { return now },
		standing: &fakeStanding{res: &standing.Result{Standing: standing.StandingOK, LongVideoPausedUntil: &until}}}
	ctx := context.Background()
	author := uuid.New()
	err := svc.requireLongVideoUploads(ctx, author, "long_video")
	var paused *UploadsPausedError
	if !errors.As(err, &paused) || !paused.Until.Equal(until) || !errors.Is(err, ErrUploadsPaused) {
		t.Fatalf("paused long video: err=%v", err)
	}
	for _, kind := range []string{"flick", "reel", "post", "poll"} {
		if err := svc.requireLongVideoUploads(ctx, author, kind); err != nil {
			t.Fatalf("%s refused by the long-video pause: %v", kind, err)
		}
	}
	past := now.Add(-time.Minute)
	svc.standing = &fakeStanding{res: &standing.Result{Standing: standing.StandingOK, LongVideoPausedUntil: &past}}
	if err := svc.requireLongVideoUploads(ctx, author, "long_video"); err != nil {
		t.Fatalf("an expired pause refused: %v", err)
	}
	if !IsTerminalCopyrightError(&UploadsPausedError{Until: until}) || !strings.HasPrefix(copyrightBlockReason(&UploadsPausedError{Until: until}), BlockReasonUploadsPaused) {
		t.Fatal("a paused background publication would be retried, not parked")
	}
	if copyrightBlockReason(ErrCopyrightReviewTier) != BlockReasonCopyrightReview || IsTerminalCopyrightError(errors.New("x")) {
		t.Fatal("block reasons")
	}
}

// CreatePost refuses a paused author's long video before anything is
// written, and leaves their reels and posts alone.
func TestCreatePost_UploadsPausedRefusesLongVideoOnly(t *testing.T) {
	r := newCreateRig(t)
	until := time.Now().Add(7 * 24 * time.Hour).UTC()
	r.svc.standing = &fakeStanding{res: &standing.Result{Standing: standing.StandingOK, LongVideoPausedUntil: &until}}
	in := r.input("long_video", nil)
	in.Title = "My long video"
	_, err := r.svc.CreatePost(context.Background(), in)
	var paused *UploadsPausedError
	if !errors.As(err, &paused) || len(r.written) != 0 {
		t.Fatalf("paused long video: err=%v writes=%d", err, len(r.written))
	}
	if _, err := r.svc.CreatePost(context.Background(), r.input("post", nil)); err != nil || len(r.written) != 1 {
		t.Fatalf("a post was refused by the long-video pause: err=%v writes=%d", err, len(r.written))
	}
}

func TestNormalizeTitleWrite(t *testing.T) {
	lang := " ta "
	w, err := normalizeTitleWrite(postgres.CopyrightTitleWrite{
		Title: "  Jailer 2 ", Aliases: []string{"Jailer II", "jailer-2", "JAILER II"}, Language: &lang,
		OwnerChannelIDs: []uuid.UUID{uuid.MustParse("11111111-1111-4111-8111-111111111111"), uuid.MustParse("11111111-1111-4111-8111-111111111111")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if w.Title != "Jailer 2" || len(w.Aliases) != 1 || w.Aliases[0] != "Jailer II" || *w.Language != "ta" || len(w.OwnerChannelIDs) != 1 {
		t.Fatalf("normalised = %+v", w)
	}
	bad := "english!"
	for name, in := range map[string]postgres.CopyrightTitleWrite{
		"empty title":       {Title: "  "},
		"one-letter title":  {Title: "M"},
		"one-letter alias":  {Title: "Jailer", Aliases: []string{"J"}},
		"bad language":      {Title: "Jailer", Language: &bad},
		"nil owner":         {Title: "Jailer", OwnerChannelIDs: []uuid.UUID{uuid.Nil}},
		"too long":          {Title: strings.Repeat("a", 201)},
		"punctuation alias": {Title: "Jailer", Aliases: []string{"!!"}},
	} {
		if _, err := normalizeTitleWrite(in); !errors.Is(err, ErrInvalidCopyrightTitle) {
			t.Errorf("%s: err=%v, want ErrInvalidCopyrightTitle", name, err)
		}
	}
}

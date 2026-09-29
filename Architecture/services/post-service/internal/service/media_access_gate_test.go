package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// The media byte gate end to end over in-memory storage: ViewerMayAccessMedia,
// ViewerMayAccessMediaBatch and ViewerMayDownloadMedia for the signed-out
// viewer (founder decision 2) and for the P-8 gaps a signed-in viewer had —
// account privacy, hidden authors, the schedule, the retired `circle`.
//
// The graph's account gate is the REAL HTTP client against the fake `can`
// endpoint from privacy_gate_test.go, so the anonymous question reaches
// graph-service as the nil viewer, exactly as production asks it.

// ── in-memory storage ──────────────────────────────────────────────────────

type fakeMediaAccessStore struct {
	facts   map[uuid.UUID]postgres.MediaAccessFacts
	avatars map[uuid.UUID]uuid.UUID
	stories map[uuid.UUID]*postgres.Story
	posts   map[uuid.UUID]*postgres.Post
	byMedia map[uuid.UUID][]uuid.UUID
}

func newFakeMediaAccessStore() *fakeMediaAccessStore {
	return &fakeMediaAccessStore{
		facts:   map[uuid.UUID]postgres.MediaAccessFacts{},
		avatars: map[uuid.UUID]uuid.UUID{},
		stories: map[uuid.UUID]*postgres.Story{},
		posts:   map[uuid.UUID]*postgres.Post{},
		byMedia: map[uuid.UUID][]uuid.UUID{},
	}
}

func (f *fakeMediaAccessStore) GetMediaAccessFacts(_ context.Context, id uuid.UUID) (*postgres.MediaAccessFacts, error) {
	facts, ok := f.facts[id]
	if !ok {
		return nil, nil
	}
	return &facts, nil
}
func (f *fakeMediaAccessStore) GetMediaAccessFactsBatch(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]postgres.MediaAccessFacts, error) {
	out := map[uuid.UUID]postgres.MediaAccessFacts{}
	for _, id := range ids {
		if facts, ok := f.facts[id]; ok {
			out[id] = facts
		}
	}
	return out, nil
}
func (f *fakeMediaAccessStore) ChannelAvatarOwners(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]uuid.UUID, error) {
	out := map[uuid.UUID]uuid.UUID{}
	for _, id := range ids {
		if owner, ok := f.avatars[id]; ok {
			out[id] = owner
		}
	}
	return out, nil
}
func (f *fakeMediaAccessStore) StoryForMedia(_ context.Context, id uuid.UUID) (*postgres.Story, error) {
	return f.stories[id], nil
}
func (f *fakeMediaAccessStore) StoriesForMediaBatch(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]*postgres.Story, error) {
	out := map[uuid.UUID]*postgres.Story{}
	for _, id := range ids {
		if st := f.stories[id]; st != nil {
			out[id] = st
		}
	}
	return out, nil
}
func (f *fakeMediaAccessStore) PostIDsByMediaID(_ context.Context, id uuid.UUID) ([]uuid.UUID, error) {
	var out []uuid.UUID
	for _, postID := range f.byMedia[id] {
		if p := f.posts[postID]; p != nil && p.DeletedAt == nil {
			out = append(out, postID)
		}
	}
	return out, nil
}
func (f *fakeMediaAccessStore) PostIDsByMediaIDs(_ context.Context, ids []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	out := map[uuid.UUID][]uuid.UUID{}
	for _, id := range ids {
		postIDs, _ := f.PostIDsByMediaID(context.Background(), id)
		if len(postIDs) > 0 {
			out[id] = postIDs
		}
	}
	return out, nil
}
func (f *fakeMediaAccessStore) GetPost(_ context.Context, id uuid.UUID) (*postgres.Post, error) {
	p := f.posts[id]
	if p == nil || p.DeletedAt != nil {
		return nil, nil
	}
	cp := *p
	return &cp, nil
}
func (f *fakeMediaAccessStore) GetPostsByIDs(_ context.Context, ids []uuid.UUID) ([]postgres.Post, error) {
	var out []postgres.Post
	for _, id := range ids {
		if p := f.posts[id]; p != nil && p.DeletedAt == nil {
			out = append(out, *p)
		}
	}
	return out, nil
}

// fixedRelationships answers the relationship batch from a map; absent
// targets are strangers.
type fixedRelationships struct {
	rels map[string]ViewerRelationship
	err  error
}

func (f fixedRelationships) Following(context.Context, string) ([]string, error) { return nil, nil }
func (f fixedRelationships) RelationshipBatch(_ context.Context, _ string, targets []string) (map[string]ViewerRelationship, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make(map[string]ViewerRelationship, len(targets))
	for _, id := range targets {
		out[id] = f.rels[id]
	}
	return out, nil
}

type fakePrivateShares struct {
	shared map[uuid.UUID]map[uuid.UUID]bool
}

func (f *fakePrivateShares) ListPrivateShares(context.Context, uuid.UUID) ([]postgres.PrivateShare, error) {
	return nil, nil
}
func (f *fakePrivateShares) ReplacePrivateShares(context.Context, uuid.UUID, uuid.UUID, []uuid.UUID) ([]postgres.PrivateShare, error) {
	return nil, nil
}
func (f *fakePrivateShares) PrivateSharedPostIDs(_ context.Context, viewer uuid.UUID, postIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := map[uuid.UUID]bool{}
	for _, id := range postIDs {
		if f.shared[id][viewer] {
			out[id] = true
		}
	}
	return out, nil
}

// ── the rig ────────────────────────────────────────────────────────────────

type mediaGateRig struct {
	svc     *Service
	store   *fakeMediaAccessStore
	hidden  *fakeHiddenAuthors
	author  uuid.UUID
	media   uuid.UUID
	post    uuid.UUID
	adult   uuid.UUID
	minor   uuid.UUID
	private uuid.UUID // an author whose account is private
}

// newMediaGateRig builds a service around one ready, moderation-passed
// asset on one public, approved, live post by `author`. `allow` is the graph
// fake's answer set for view_posts (self-view is always true); the rig's
// private author is denied there for every viewer.
func newMediaGateRig(t *testing.T) *mediaGateRig {
	t.Helper()
	r := &mediaGateRig{
		store: newFakeMediaAccessStore(), hidden: &fakeHiddenAuthors{hidden: map[uuid.UUID]bool{}},
		author: uuid.New(), media: uuid.New(), post: uuid.New(), adult: uuid.New(), minor: uuid.New(), private: uuid.New(),
	}
	var calls int32
	srv := fakeGraphCan(t, map[string]bool{r.author.String(): true, r.private.String(): false}, &calls, nil)
	t.Cleanup(srv.Close)
	r.store.facts[r.media] = postgres.MediaAccessFacts{UploaderID: r.author, ProcessingStatus: "ready", ModerationStatus: "passed"}
	r.store.posts[r.post] = &postgres.Post{ID: r.post, AuthorID: r.author, Visibility: "public", ReviewStatus: "approved", AllowDownload: true}
	r.store.byMedia[r.media] = []uuid.UUID{r.post}
	r.svc = &Service{
		mediaAccess:        r.store,
		storyAudience:      NewStoryAudience(fixedRelationships{rels: map[string]ViewerRelationship{}}),
		graphServiceURL:    srv.URL,
		internalServiceKey: "test-key",
		httpClient:         http.DefaultClient,
		birthDates:         &fakeBirthDates{dob: map[uuid.UUID]*time.Time{r.adult: dayp(1990, 1, 1), r.minor: dayp(2015, 1, 1)}},
		privateShare:       &fakePrivateShares{shared: map[uuid.UUID]map[uuid.UUID]bool{}},
		now:                func() time.Time { return hubNow },
	}
	r.svc.hiddenAuthors = r.hidden
	return r
}

func (r *mediaGateRig) single(t *testing.T, viewer uuid.UUID) MediaAccessResult {
	t.Helper()
	res, err := r.svc.ViewerMayAccessMedia(context.Background(), viewer, r.media)
	if err != nil {
		t.Fatalf("single: %v", err)
	}
	return res
}

func (r *mediaGateRig) batch(t *testing.T, viewer uuid.UUID) MediaAccessResult {
	t.Helper()
	res, err := r.svc.ViewerMayAccessMediaBatch(context.Background(), viewer, []uuid.UUID{r.media})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	out, ok := res[r.media]
	if !ok {
		t.Fatal("batch answered nothing for the asset")
	}
	return out
}

// both runs the single and the batch path and insists they agree.
func (r *mediaGateRig) both(t *testing.T, viewer uuid.UUID) bool {
	t.Helper()
	s, b := r.single(t, viewer), r.batch(t, viewer)
	if s.Allowed != b.Allowed {
		t.Fatalf("single (%v %s) and batch (%v %s) disagree", s.Allowed, s.Reason, b.Allowed, b.Reason)
	}
	return s.Allowed
}

// ── signed-out ─────────────────────────────────────────────────────────────

func TestAnonymousPlaysAPublicPostsMedia(t *testing.T) {
	r := newMediaGateRig(t)
	res := r.single(t, uuid.Nil)
	if !res.Allowed || res.Decision != DecisionAllowed || res.Reason != "post_allowed" {
		t.Fatalf("anonymous refused a public post's media: %+v", res)
	}
	if b := r.batch(t, uuid.Nil); !b.Allowed || b.Reason != "post_allowed" {
		t.Fatalf("batch refused the anonymous viewer: %+v", b)
	}
	// One question to the graph as the nil stranger; the store answered
	// with a public account.
}

func TestAnonymousIsRefusedEverythingShortOfPublic(t *testing.T) {
	r := newMediaGateRig(t)
	cases := map[string]func(p *postgres.Post){
		"unlisted":       func(p *postgres.Post) { p.Visibility = "unlisted" },
		"followers":      func(p *postgres.Post) { p.Visibility = "followers" },
		"private":        func(p *postgres.Post) { p.Visibility = "private" },
		"staged":         func(p *postgres.Post) { p.Visibility = "staged" },
		"pending review": func(p *postgres.Post) { p.ReviewStatus = "pending" },
		"scheduled":      func(p *postgres.Post) { p.PublishAt = dayp(2030, 1, 1) },
		"age-restricted": func(p *postgres.Post) { p.AgeRestricted = true },
		"members-only":   func(p *postgres.Post) { id := uuid.New(); p.TierRequiredID = &id },
		"soft-deleted":   func(p *postgres.Post) { p.DeletedAt = dayp(2026, 9, 1) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := *r.store.posts[r.post]
			mutate(&p)
			r.store.posts[r.post] = &p
			defer func() {
				r.store.posts[r.post] = &postgres.Post{ID: r.post, AuthorID: r.author, Visibility: "public", ReviewStatus: "approved", AllowDownload: true}
			}()
			if r.both(t, uuid.Nil) {
				t.Fatalf("anonymous played a %s post's media", name)
			}
			if res := r.single(t, uuid.Nil); res.Reason != "no_public_post" && res.Reason != "no_visible_post_or_story" {
				t.Fatalf("unexpected reason %q", res.Reason)
			}
		})
	}
}

func TestAnonymousIsRefusedAPrivateAccountsPublicPost(t *testing.T) {
	r := newMediaGateRig(t)
	r.store.posts[r.post].AuthorID = r.private
	if r.both(t, uuid.Nil) {
		t.Fatal("anonymous played a private account's post")
	}
}

func TestAnonymousIsRefusedAHiddenAuthorsPost(t *testing.T) {
	r := newMediaGateRig(t)
	r.hidden.hidden[r.author] = true
	if r.both(t, uuid.Nil) {
		t.Fatal("anonymous played a deactivated / pending-deletion author's post")
	}
}

// A graph outage or a hidden-authors lookup failure denies the anonymous
// viewer rather than treating the account as public.
func TestAnonymousAccountGateFailsClosed(t *testing.T) {
	r := newMediaGateRig(t)
	r.hidden.err = errors.New("db down")
	if r.both(t, uuid.Nil) {
		t.Fatal("anonymous played while the hidden-authors lookup was failing")
	}
}

// Stories have no anonymous reading, and a channel avatar has no anonymous
// refusal (it was already public to every signed-in viewer).
func TestAnonymousStoriesAndAvatars(t *testing.T) {
	r := newMediaGateRig(t)
	storyMedia := uuid.New()
	r.store.facts[storyMedia] = postgres.MediaAccessFacts{UploaderID: r.author, ProcessingStatus: "ready", ModerationStatus: "passed"}
	r.store.stories[storyMedia] = &postgres.Story{ID: uuid.New(), AuthorID: r.author, Visibility: StoryVisibilityPublic, ModerationState: StoryModerationApproved, ExpiresAt: time.Now().Add(time.Hour)}
	if res, err := r.svc.ViewerMayAccessMedia(context.Background(), uuid.Nil, storyMedia); err != nil || res.Allowed {
		t.Fatalf("anonymous read a story's media: %+v %v", res, err)
	}
	if res, err := r.svc.ViewerMayAccessMediaBatch(context.Background(), uuid.Nil, []uuid.UUID{storyMedia}); err != nil || res[storyMedia].Allowed {
		t.Fatalf("anonymous read a story's media in batch: %+v %v", res, err)
	}
	if res, err := r.svc.ViewerMayAccessMedia(context.Background(), r.adult, storyMedia); err != nil || !res.Allowed {
		t.Fatalf("signed-in viewer refused a public story's media: %+v %v", res, err)
	}
	avatar := uuid.New()
	r.store.facts[avatar] = postgres.MediaAccessFacts{UploaderID: r.author, ProcessingStatus: "ready", ModerationStatus: "passed"}
	r.store.avatars[avatar] = uuid.New()
	if res, err := r.svc.ViewerMayAccessMedia(context.Background(), uuid.Nil, avatar); err != nil || !res.Allowed || res.Reason != "channel_avatar" {
		t.Fatalf("anonymous refused a channel avatar: %+v %v", res, err)
	}
}

// Moderation is not waived for a public post: the canonical gate runs first.
func TestAnonymousNeverBypassesTheCanonicalMediaGate(t *testing.T) {
	r := newMediaGateRig(t)
	for _, moderation := range []string{"pending", "manual_review", "rejected", ""} {
		r.store.facts[r.media] = postgres.MediaAccessFacts{UploaderID: r.author, ProcessingStatus: "ready", ModerationStatus: moderation}
		if res := r.single(t, uuid.Nil); res.Allowed || res.Reason != "moderation_not_approved" {
			t.Fatalf("moderation %q: %+v", moderation, res)
		}
	}
}

// A signed-out viewer never downloads, whatever the post allows.
func TestAnonymousNeverDownloads(t *testing.T) {
	r := newMediaGateRig(t)
	ok, err := r.svc.ViewerMayDownloadMedia(context.Background(), uuid.Nil, r.media)
	if err != nil || ok {
		t.Fatalf("anonymous download: %v %v", ok, err)
	}
}

// ── signed-in gap fixes (P-8 a, c) ─────────────────────────────────────────

func TestSignedInViewerIsRefusedAPrivateAccountsPost(t *testing.T) {
	r := newMediaGateRig(t)
	r.store.posts[r.post].AuthorID = r.private
	if r.both(t, r.adult) {
		t.Fatal("a stranger played a private account's post")
	}
	if ok, err := r.svc.ViewerMayDownloadMedia(context.Background(), r.adult, r.media); err != nil || ok {
		t.Fatalf("a stranger downloaded a private account's post: %v %v", ok, err)
	}
	// The account's owner still plays their own.
	if !r.both(t, r.private) {
		t.Fatal("the private account's owner was refused their own post")
	}
}

func TestSignedInViewerIsRefusedAHiddenAuthorsPost(t *testing.T) {
	r := newMediaGateRig(t)
	r.hidden.hidden[r.author] = true
	if r.both(t, r.adult) {
		t.Fatal("a viewer played a deactivated / pending-deletion author's post")
	}
	if ok, _ := r.svc.ViewerMayDownloadMedia(context.Background(), r.adult, r.media); ok {
		t.Fatal("a viewer downloaded a hidden author's post")
	}
	if !r.both(t, r.author) {
		t.Fatal("the hidden author was refused their own post")
	}
}

func TestSignedInViewerIsRefusedAScheduledPost(t *testing.T) {
	r := newMediaGateRig(t)
	r.store.posts[r.post].PublishAt = dayp(2030, 1, 1)
	if r.both(t, r.adult) {
		t.Fatal("a viewer played a scheduled post before its time")
	}
	if ok, _ := r.svc.ViewerMayDownloadMedia(context.Background(), r.adult, r.media); ok {
		t.Fatal("a viewer downloaded a scheduled post")
	}
	if !r.both(t, r.author) {
		t.Fatal("the author was refused their own scheduled post")
	}
}

// The retired `circle` audience: author-only on the media gate, as on the
// detail gate (TestDetailGateCircleIsAuthorOnly in privacy_gate_test.go's
// neighbourhood), even for a follower.
func TestCircleIsAuthorOnlyOnTheMediaGate(t *testing.T) {
	r := newMediaGateRig(t)
	r.store.posts[r.post].Visibility = "circle"
	r.svc.storyAudience = NewStoryAudience(fixedRelationships{rels: map[string]ViewerRelationship{r.author.String(): {Follows: true}}})
	if r.both(t, r.adult) {
		t.Fatal("a follower played a circle post")
	}
	if !r.both(t, r.author) {
		t.Fatal("the author was refused their own circle post")
	}
}

// The age gate still binds a signed-in viewer; an unwired service is
// unresolved rather than permissive.
func TestSignedInAgeGateAndUnwiredStore(t *testing.T) {
	r := newMediaGateRig(t)
	r.store.posts[r.post].AgeRestricted = true
	if r.both(t, r.minor) {
		t.Fatal("a minor played an 18+ post")
	}
	if !r.both(t, r.adult) {
		t.Fatal("an adult was refused an 18+ public post")
	}
	unwired := &Service{}
	if _, err := unwired.ViewerMayAccessMedia(context.Background(), r.adult, r.media); !errors.Is(err, ErrStoryPolicyUnresolved) {
		t.Fatalf("unwired single: %v", err)
	}
	if _, err := unwired.ViewerMayAccessMediaBatch(context.Background(), uuid.Nil, []uuid.UUID{r.media}); !errors.Is(err, ErrStoryPolicyUnresolved) {
		t.Fatalf("unwired batch: %v", err)
	}
}

// The detail gate's twin: `circle` no longer reads as followers there either.
func TestDetailGateCircleIsAuthorOnly(t *testing.T) {
	author, follower := uuid.New(), uuid.New()
	// graphServiceURL empty: the follow check would answer true, so a
	// "followers"-style reading of circle would let the follower in.
	s := &Service{}
	p := &postgres.Post{ID: uuid.New(), AuthorID: author, Visibility: "circle", ReviewStatus: "approved"}
	if s.viewerMayViewPost(context.Background(), p, &follower) {
		t.Fatal("the detail gate opened a circle post to a follower")
	}
	if !s.viewerMayViewPost(context.Background(), p, &author) {
		t.Fatal("the detail gate refused the author their own circle post")
	}
	p.Visibility = "followers"
	if !s.viewerMayViewPost(context.Background(), p, &follower) {
		t.Fatal("followers stopped working on the detail gate")
	}
}

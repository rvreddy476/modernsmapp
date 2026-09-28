package service

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Edit after publish (2026-09-27): the owner guard and every validation,
// driven through an in-memory postEditStore so the refusals are pinned
// without a database. The store's own ownership re-check is covered by the
// integration test.

type fakePostEditStore struct {
	posts   map[uuid.UUID]*postgres.Post
	media   map[uuid.UUID]postgres.MediaOwnership
	updates []postgres.PostEditPatch
	written []uuid.UUID
	// failWrite makes UpdatePostFields fail for one post (bulk isolation).
	failWrite map[uuid.UUID]error
	counts    postgres.CreatorCounts
	catSet    map[uuid.UUID]string
}

func newFakePostEditStore() *fakePostEditStore {
	return &fakePostEditStore{
		posts:     map[uuid.UUID]*postgres.Post{},
		media:     map[uuid.UUID]postgres.MediaOwnership{},
		catSet:    map[uuid.UUID]string{},
		failWrite: map[uuid.UUID]error{},
	}
}

func (f *fakePostEditStore) GetPost(_ context.Context, id uuid.UUID) (*postgres.Post, error) {
	p, ok := f.posts[id]
	if !ok {
		return nil, nil
	}
	cp := *p
	return &cp, nil
}

func (f *fakePostEditStore) UpdatePostFields(_ context.Context, postID, actorID uuid.UUID, patch postgres.PostEditPatch) (*postgres.Post, error) {
	p, ok := f.posts[postID]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	if p.AuthorID != actorID {
		return nil, postgres.ErrPostEditNotOwned
	}
	if err := f.failWrite[postID]; err != nil {
		return nil, err
	}
	f.updates = append(f.updates, patch)
	f.written = append(f.written, postID)
	if patch.Title != nil {
		p.Title = *patch.Title
	}
	if patch.Visibility != nil {
		p.Visibility = *patch.Visibility
	}
	if patch.Category != nil {
		p.Category = *patch.Category
	}
	if patch.Hashtags != nil {
		p.Hashtags = *patch.Hashtags
	}
	if patch.Tags != nil {
		p.Tags = *patch.Tags
	}
	if patch.AgeRestricted != nil {
		p.AgeRestricted = *patch.AgeRestricted
	}
	if patch.HideLikeCount != nil {
		p.HideLikeCount = *patch.HideLikeCount
	}
	if patch.DefaultCommentSort != nil {
		p.DefaultCommentSort = *patch.DefaultCommentSort
	}
	if patch.License != nil {
		p.License = *patch.License
	}
	if patch.ClearRelatedPost {
		p.RelatedPostID = nil
	} else if patch.RelatedPostID != nil {
		id := *patch.RelatedPostID
		p.RelatedPostID = &id
	}
	if patch.Distribution != nil {
		p.Distribution = patch.Distribution
		p.DistributionRev++
	}
	cp := *p
	return &cp, nil
}

func (f *fakePostEditStore) BatchGetMediaOwnership(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]postgres.MediaOwnership, error) {
	out := map[uuid.UUID]postgres.MediaOwnership{}
	for _, id := range ids {
		if m, ok := f.media[id]; ok {
			out[id] = m
		}
	}
	return out, nil
}

func (f *fakePostEditStore) PostAuthorsByIDs(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]uuid.UUID, error) {
	out := map[uuid.UUID]uuid.UUID{}
	for _, id := range ids {
		if p, ok := f.posts[id]; ok {
			out[id] = p.AuthorID
		}
	}
	return out, nil
}

func (f *fakePostEditStore) CountCreatorContent(_ context.Context, _ uuid.UUID) (postgres.CreatorCounts, error) {
	return f.counts, nil
}

func (f *fakePostEditStore) UpdatePostCategory(_ context.Context, postID uuid.UUID, category string) error {
	f.catSet[postID] = category
	return nil
}

func newEditRig(t *testing.T) (*Service, *fakePostEditStore, uuid.UUID, *postgres.Post) {
	t.Helper()
	store := newFakePostEditStore()
	owner := uuid.New()
	post := &postgres.Post{
		ID: uuid.New(), AuthorID: owner, ContentType: "long_video", Visibility: "public",
		Title: "Original", Text: "hello #one", Hashtags: []string{"one"},
		Media: []postgres.PostMedia{{MediaID: uuid.New(), Kind: "video"}},
	}
	store.posts[post.ID] = post
	svc := &Service{postEdits: store}
	return svc, store, owner, post
}

func str(s string) *string { return &s }
func boolp(b bool) *bool   { return &b }

func TestUpdatePostIsOwnerOnly(t *testing.T) {
	svc, store, owner, post := newEditRig(t)
	ctx := context.Background()

	if _, err := svc.UpdatePost(ctx, uuid.New(), post.ID, PostEditInput{Title: str("Stolen")}); !errors.Is(err, ErrNotPostAuthor) {
		t.Fatalf("stranger edit: err=%v want ErrNotPostAuthor", err)
	}
	if _, err := svc.UpdatePost(ctx, uuid.Nil, post.ID, PostEditInput{Title: str("Anon")}); !errors.Is(err, ErrNotPostAuthor) {
		t.Fatalf("anonymous edit: err=%v want ErrNotPostAuthor", err)
	}
	if _, err := svc.UpdatePost(ctx, owner, uuid.New(), PostEditInput{Title: str("Ghost")}); !errors.Is(err, ErrPostNotFound) {
		t.Fatalf("missing post: err=%v want ErrPostNotFound", err)
	}
	if len(store.updates) != 0 {
		t.Fatalf("a refused edit reached the store: %+v", store.updates)
	}
	detail, err := svc.UpdatePost(ctx, owner, post.ID, PostEditInput{Title: str("  Renamed  ")})
	if err != nil {
		t.Fatalf("owner edit: %v", err)
	}
	if detail.Title != "Renamed" {
		t.Fatalf("title=%q want Renamed (trimmed)", detail.Title)
	}
	// Unwired store fails closed.
	if _, err := (&Service{}).UpdatePost(ctx, owner, post.ID, PostEditInput{}); !errors.Is(err, ErrAuthoringStoreUnavailable) {
		t.Fatalf("unwired: err=%v", err)
	}
}

func TestUpdatePostValidatesEveryField(t *testing.T) {
	svc, _, owner, post := newEditRig(t)
	ctx := context.Background()
	long := make([]rune, MaxTitleRunes+1)
	for i := range long {
		long[i] = 'x'
	}
	cases := []struct {
		name string
		in   PostEditInput
		want error
	}{
		{"category outside the long list", PostEditInput{Category: str("Film & Animation")}, ErrInvalidCategory},
		{"visibility outside the create enum", PostEditInput{Visibility: str("staged")}, ErrInvalidVisibility},
		{"title too long", PostEditInput{Title: str(string(long))}, ErrTitleTooLong},
		{"long video needs a title", PostEditInput{Title: str("   ")}, ErrTitleRequired},
		{"hashtag alphabet", PostEditInput{Hashtags: &[]string{"bad tag!"}}, ErrInvalidHashtag},
		{"language shape", PostEditInput{Language: str("english-language-x")}, ErrInvalidLanguage},
		{"too many tags", PostEditInput{Tags: &[]string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12", "13", "14", "15", "16", "17", "18", "19", "20", "21"}}, ErrTooManyTags},
		{"cover must exist", PostEditInput{CoverMediaID: ptrUUID(uuid.New())}, ErrMediaNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.UpdatePost(ctx, owner, post.ID, tc.in)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
		})
	}
	// Accepted values are canonicalised.
	detail, err := svc.UpdatePost(ctx, owner, post.ID, PostEditInput{
		Category: str(" Podcasts "), Visibility: str("Unlisted"), Hashtags: &[]string{"#Two"},
	})
	if err != nil {
		t.Fatalf("valid edit: %v", err)
	}
	if detail.Category != "podcasts" || detail.Visibility != "unlisted" {
		t.Fatalf("category=%q visibility=%q", detail.Category, detail.Visibility)
	}
	// Text-parsed and explicit hashtags are merged, as on create.
	if got := detail.Hashtags; len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("hashtags=%v want [one two]", got)
	}
}

func TestUpdatePostCoverMustBeTheOwnersImage(t *testing.T) {
	svc, store, owner, post := newEditRig(t)
	ctx := context.Background()
	foreign := uuid.New()
	store.media[foreign] = postgres.MediaOwnership{UploaderID: uuid.New(), Kind: "image", ProcessingStatus: "ready", ModerationStatus: "passed"}
	video := uuid.New()
	store.media[video] = postgres.MediaOwnership{UploaderID: owner, Kind: "video", ProcessingStatus: "ready", ModerationStatus: "passed"}
	image := uuid.New()
	store.media[image] = postgres.MediaOwnership{UploaderID: owner, Kind: "image", ProcessingStatus: "ready", ModerationStatus: "passed"}

	if _, err := svc.UpdatePost(ctx, owner, post.ID, PostEditInput{CoverMediaID: &foreign}); !errors.Is(err, ErrMediaNotOwned) {
		t.Fatalf("foreign cover: err=%v want ErrMediaNotOwned", err)
	}
	if _, err := svc.UpdatePost(ctx, owner, post.ID, PostEditInput{CoverMediaID: &video}); !errors.Is(err, ErrMediaTypeMismatch) {
		t.Fatalf("video cover: err=%v want ErrMediaTypeMismatch", err)
	}
	if _, err := svc.UpdatePost(ctx, owner, post.ID, PostEditInput{CoverMediaID: &image}); err != nil {
		t.Fatalf("own image cover: %v", err)
	}
	if last := store.updates[len(store.updates)-1]; last.CoverMediaID == nil || *last.CoverMediaID != image {
		t.Fatalf("cover not in patch: %+v", last)
	}
}

func TestSetPostCategoryIsOwnerOnlyAndTaxonomyBound(t *testing.T) {
	svc, store, owner, post := newEditRig(t)
	ctx := context.Background()
	if err := svc.SetPostCategory(ctx, uuid.New(), post.ID, "kids"); !errors.Is(err, ErrNotPostAuthor) {
		t.Fatalf("stranger: %v", err)
	}
	if err := svc.SetPostCategory(ctx, owner, uuid.New(), "kids"); !errors.Is(err, ErrPostNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if err := svc.SetPostCategory(ctx, owner, post.ID, "Howto & Style"); !errors.Is(err, ErrInvalidCategory) {
		t.Fatalf("free text: %v", err)
	}
	if err := svc.SetPostCategory(ctx, owner, post.ID, ""); !errors.Is(err, ErrInvalidCategory) {
		t.Fatalf("empty: %v", err)
	}
	if err := svc.SetPostCategory(ctx, owner, post.ID, "Kids"); err != nil || store.catSet[post.ID] != "kids" {
		t.Fatalf("valid: err=%v stored=%q", err, store.catSet[post.ID])
	}
}

func TestCreatorSummaryReadsFollowersFromTheChannel(t *testing.T) {
	svc, store, owner, _ := newEditRig(t)
	store.counts = postgres.CreatorCounts{Videos: 3, Shorts: 2, Live: 1, Collections: 4}
	ctx := context.Background()
	got, err := svc.GetCreatorSummary(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if got.Followers != 0 || got.Videos != 3 || got.Collections != 4 {
		t.Fatalf("without channel: %+v", got)
	}
	channels := newFakeChannelStore()
	channels.byUser[owner] = &postgres.Channel{UserID: owner, Handle: "own", Name: "Own", SubscriberCount: 42}
	svc.channels = channels
	got, err = svc.GetCreatorSummary(ctx, owner)
	if err != nil || got.Followers != 42 {
		t.Fatalf("with channel: %+v err=%v", got, err)
	}
}

func ptrUUID(id uuid.UUID) *uuid.UUID { return &id }

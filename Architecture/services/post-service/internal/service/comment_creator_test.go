package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Creator comment tools (2026-09-27): heart and pin are the POST author's
// alone; a reply cannot be pinned; the inbox validates its filters.

type fakeCreatorCommentStore struct {
	refs    map[uuid.UUID]*postgres.CommentOwnerRef
	hearts  map[uuid.UUID]bool
	pinned  map[uuid.UUID]uuid.UUID // post -> comment
	inbox   []postgres.InboxRow
	lastQ   *postgres.InboxQuery
	written int
}

func newFakeCreatorCommentStore() *fakeCreatorCommentStore {
	return &fakeCreatorCommentStore{refs: map[uuid.UUID]*postgres.CommentOwnerRef{}, hearts: map[uuid.UUID]bool{}, pinned: map[uuid.UUID]uuid.UUID{}}
}

func (f *fakeCreatorCommentStore) GetCommentOwnerRef(_ context.Context, id uuid.UUID) (*postgres.CommentOwnerRef, error) {
	ref, ok := f.refs[id]
	if !ok {
		return nil, fmt.Errorf("COMMENT_NOT_FOUND")
	}
	return ref, nil
}

func (f *fakeCreatorCommentStore) SetCommentHeart(_ context.Context, id uuid.UUID, on bool) error {
	f.written++
	f.hearts[id] = on
	return nil
}

func (f *fakeCreatorCommentStore) PinComment(_ context.Context, postID, id uuid.UUID) error {
	f.written++
	f.pinned[postID] = id
	return nil
}

func (f *fakeCreatorCommentStore) UnpinComment(_ context.Context, id uuid.UUID) error {
	f.written++
	for post, c := range f.pinned {
		if c == id {
			delete(f.pinned, post)
		}
	}
	return nil
}

func (f *fakeCreatorCommentStore) ListCreatorInbox(_ context.Context, q postgres.InboxQuery) ([]postgres.InboxRow, string, error) {
	f.lastQ = &q
	return f.inbox, "", nil
}

func TestHeartAndPinArePostAuthorOnly(t *testing.T) {
	store := newFakeCreatorCommentStore()
	svc := &Service{creatorComments: store}
	ctx := context.Background()
	author, commenter, stranger := uuid.New(), uuid.New(), uuid.New()
	post := uuid.New()
	top := uuid.New()
	reply := uuid.New()
	store.refs[top] = &postgres.CommentOwnerRef{CommentID: top, PostID: post, PostAuthor: author}
	store.refs[reply] = &postgres.CommentOwnerRef{CommentID: reply, PostID: post, PostAuthor: author, ParentID: &top}

	for _, who := range []uuid.UUID{commenter, stranger, uuid.Nil} {
		if _, err := svc.SetCommentHeart(ctx, who, top, true); !errors.Is(err, ErrNotPostAuthor) {
			t.Fatalf("heart by %s: err=%v want ErrNotPostAuthor", who, err)
		}
		if _, err := svc.PinComment(ctx, who, top); !errors.Is(err, ErrNotPostAuthor) {
			t.Fatalf("pin by %s: err=%v want ErrNotPostAuthor", who, err)
		}
		if _, err := svc.UnpinComment(ctx, who, top); !errors.Is(err, ErrNotPostAuthor) {
			t.Fatalf("unpin by %s: err=%v want ErrNotPostAuthor", who, err)
		}
	}
	if store.written != 0 {
		t.Fatalf("a refused write reached the store (%d)", store.written)
	}
	if _, err := svc.SetCommentHeart(ctx, author, uuid.New(), true); err == nil || err.Error() != "COMMENT_NOT_FOUND" {
		t.Fatalf("missing comment: %v", err)
	}
	res, err := svc.SetCommentHeart(ctx, author, top, true)
	if err != nil || !res.HeartedByAuthor || !store.hearts[top] {
		t.Fatalf("author heart: %+v %v", res, err)
	}
	if _, err := svc.PinComment(ctx, author, reply); !errors.Is(err, ErrCannotPinReply) {
		t.Fatalf("pin a reply: err=%v want ErrCannotPinReply", err)
	}
	pin, err := svc.PinComment(ctx, author, top)
	if err != nil || !pin.Pinned || pin.PostID != post || store.pinned[post] != top {
		t.Fatalf("author pin: %+v %v", pin, err)
	}
	un, err := svc.UnpinComment(ctx, author, top)
	if err != nil || un.Pinned || store.pinned[post] != uuid.Nil {
		t.Fatalf("author unpin: %+v %v", un, err)
	}
	if _, err := (&Service{}).PinComment(ctx, author, top); !errors.Is(err, ErrAuthoringStoreUnavailable) {
		t.Fatalf("unwired: %v", err)
	}
}

func TestCreatorInboxValidatesAndDefaults(t *testing.T) {
	store := newFakeCreatorCommentStore()
	svc := &Service{creatorComments: store}
	ctx := context.Background()
	me := uuid.New()

	if _, _, err := svc.ListCreatorInbox(ctx, me, CreatorInboxQuery{Status: "read"}); !errors.Is(err, ErrInvalidInboxStatus) {
		t.Fatalf("status: %v", err)
	}
	if _, _, err := svc.ListCreatorInbox(ctx, me, CreatorInboxQuery{Content: "stories"}); !errors.Is(err, ErrInvalidInboxContent) {
		t.Fatalf("content: %v", err)
	}
	if _, _, err := svc.ListCreatorInbox(ctx, me, CreatorInboxQuery{Sort: "oldest"}); !errors.Is(err, ErrInvalidInboxSort) {
		t.Fatalf("sort: %v", err)
	}
	rows, _, err := svc.ListCreatorInbox(ctx, me, CreatorInboxQuery{Content: "videos", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if rows == nil {
		t.Fatal("empty inbox must be [] not null")
	}
	q := store.lastQ
	if q.AuthorID != me || q.Status != postgres.InboxStatusUnanswered || q.Sort != postgres.InboxSortNewest || q.Limit != 10 {
		t.Fatalf("defaults: %+v", q)
	}
	if len(q.ContentTypes) != 2 || q.ContentTypes[0] != "long_video" {
		t.Fatalf("content types: %v", q.ContentTypes)
	}
	if _, _, err := (&Service{}).ListCreatorInbox(ctx, me, CreatorInboxQuery{}); !errors.Is(err, ErrAuthoringStoreUnavailable) {
		t.Fatalf("unwired: %v", err)
	}
}

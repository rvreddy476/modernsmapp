//go:build integration

package events

import (
	"context"
	"testing"
	"time"

	"github.com/atpost/search-service/internal/store/search"
	"github.com/google/uuid"
)

// Audit 2026-11-07, S-M1 / S-M3, against live OpenSearch: the Painless
// halves the unit tests can only read as text. A lifecycle hide must
// survive every rewrite by a writer that cannot see it.

func TestOpenSearch_HiddenUserSurvivesEveryRewrite(t *testing.T) {
	store := liveStore(t)
	ctx := context.Background()
	id := uuid.New().String()
	marker := "zz" + uuid.New().String()[:8]
	t.Cleanup(func() { _ = store.DeleteUser(context.Background(), id) })

	if err := store.IndexUser(ctx, search.UserDoc{UserID: id, Username: marker, DisplayName: marker}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateUserHidden(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	// The profile event and the reindex both rewrite the whole document.
	if err := store.IndexUser(ctx, search.UserDoc{UserID: id, Username: marker, DisplayName: "edited " + marker}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BulkIndexUsers(ctx, []search.UserDoc{{UserID: id, Username: marker, DisplayName: marker}}); err != nil {
		t.Fatal(err)
	}
	if err := store.RefreshIndex(ctx, search.IndexUsers); err != nil {
		t.Fatal(err)
	}
	if got, err := store.GetUsersByIDs(ctx, []string{id}); err != nil || len(got) != 0 {
		t.Fatalf("a deactivated account came back after a rewrite: %v (err %v)", got, err)
	}

	// Only the lifecycle path brings it back.
	if err := store.UpdateUserHidden(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	_ = store.RefreshIndex(ctx, search.IndexUsers)
	if got, _ := store.GetUsersByIDs(ctx, []string{id}); len(got) != 1 {
		t.Fatal("reactivation did not unhide the account")
	}
}

func TestOpenSearch_HideBeforeFirstIndexStaysHidden(t *testing.T) {
	store := liveStore(t)
	ctx := context.Background()
	id := uuid.New().String()
	t.Cleanup(func() { _ = store.DeleteUser(context.Background(), id) })

	if err := store.UpdateUserHidden(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	if err := store.IndexUser(ctx, search.UserDoc{UserID: id, Username: "late"}); err != nil {
		t.Fatal(err)
	}
	_ = store.RefreshIndex(ctx, search.IndexUsers)
	if got, _ := store.GetUsersByIDs(ctx, []string{id}); len(got) != 0 {
		t.Fatal("a user hidden before their first document was indexed visible")
	}
}

func TestOpenSearch_HiddenAuthorPostSurvivesReprojectionAndReapproval(t *testing.T) {
	store := liveStore(t)
	ctx := context.Background()
	author := uuid.New().String()
	post := uuid.New().String()
	marker := "zz" + uuid.New().String()[:8]
	t.Cleanup(func() { _ = store.DeletePost(context.Background(), post) })

	doc := search.PostDoc{PostID: post, AuthorID: author, Text: "hidden " + marker,
		Visibility: "public", ReviewStatus: "approved", CreatedAt: time.Now().UTC()}
	write := func(p search.PostProjection) {
		t.Helper()
		if err := store.ApplyPostProjection(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	write(search.PostProjection{PostID: post, Rev: 1, Doc: doc, Author: &search.AuthorVisibility{}})
	if err := store.UpdatePostsAuthorHidden(ctx, author, true); err != nil {
		t.Fatal(err)
	}
	// The reconciler re-projects; a moderator removes and then re-approves.
	write(search.PostProjection{PostID: post, Rev: 1, Reproject: true, Doc: doc, Author: &search.AuthorVisibility{}})
	write(search.PostProjection{PostID: post, Rev: 2, Removed: true, AuthorID: author})
	write(search.PostProjection{PostID: post, Rev: 3, Doc: doc, Author: &search.AuthorVisibility{}})
	refresh(t, store)
	if found, _ := store.SearchPostsFiltered(ctx, marker, nil, 10); len(found) != 0 {
		t.Fatalf("a deactivated author's post came back: %+v", found)
	}
}

func TestOpenSearch_PrivateAuthorFlagIsWrittenFromTheAuthor(t *testing.T) {
	store := liveStore(t)
	ctx := context.Background()
	post := uuid.New().String()
	marker := "zz" + uuid.New().String()[:8]
	t.Cleanup(func() { _ = store.DeletePost(context.Background(), post) })

	if err := store.ApplyPostProjection(ctx, search.PostProjection{
		PostID: post, Rev: 1,
		Doc: search.PostDoc{PostID: post, AuthorID: uuid.New().String(), Text: "private " + marker,
			Visibility: "public", ReviewStatus: "approved", CreatedAt: time.Now().UTC()},
		Author: &search.AuthorVisibility{IsPrivate: true},
	}); err != nil {
		t.Fatal(err)
	}
	refresh(t, store)
	anon := search.WithBlockedIDs(ctx, nil)
	if found, _ := store.SearchPostsFiltered(anon, marker, nil, 10); len(found) != 0 {
		t.Fatalf("a private author's post is visible to a stranger: %+v", found)
	}
}

func TestOpenSearch_OwnerTubeHideSurvivesTheReindex(t *testing.T) {
	store := liveStore(t)
	ctx := context.Background()
	owner := uuid.New().String()
	channel := uuid.New().String()
	marker := "zz" + uuid.New().String()[:8]
	t.Cleanup(func() { _ = store.DeleteTubeChannel(context.Background(), channel) })

	doc := search.TubeChannelDoc{ChannelID: channel, OwnerID: owner, Name: marker, Handle: marker,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if _, err := store.BulkIndexTubeChannels(ctx, []search.TubeChannelDoc{doc}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateOwnerTubeHidden(ctx, owner, true); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BulkIndexTubeChannels(ctx, []search.TubeChannelDoc{doc}); err != nil {
		t.Fatal(err)
	}
	_ = store.RefreshIndex(ctx, search.IndexTubeChannels)
	if found, _ := store.SearchTubeChannels(ctx, marker, 10, 0); len(found) != 0 {
		t.Fatalf("a deactivated person's channel is searchable: %+v", found)
	}

	if err := store.DeleteOwnerEntityDocs(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateOwnerTubeHidden(ctx, owner, false); err != nil {
		t.Fatal(err)
	}
	_ = store.RefreshIndex(ctx, search.IndexTubeChannels)
	if found, _ := store.SearchTubeChannels(ctx, marker, 10, 0); len(found) != 0 {
		t.Fatalf("an erased person's channel survived erasure: %+v", found)
	}
}

//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atpost/post-service/internal/store/postgres"
)

/*
Replies for everyone and emoji reactions on comments (2026-09-27), against a
real Postgres (*_test only).

Run:
  POSTGRES_DSN=…/post_it_test go test -tags integration ./internal/store/postgres/ -run 'Reply|Reaction|LegacyLike' -v

Mutation checks — the assertion that fails if:
  - the post-owner rule comes back:   TestReplyIsOpenToEveryViewer / "a non-owner could not reply"
  - a second reply is refused:        TestReplyIsOpenToEveryViewer / "a second reply on the same comment was refused"
  - a second emoji ADDS instead of replacing:
                                       TestCommentReactionReplacesDeletesAndSummarises / "a second emoji added a reaction instead of replacing"
*/

func seedReplyPost(t *testing.T, pool *pgxpool.Pool, author uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	postID := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO posts (id, author_id, text, visibility, content_type, created_at, updated_at) VALUES ($1, $2, 'thread me', 'public', 'post', now(), now())`,
		postID, author); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM comment_reactions WHERE comment_id IN (SELECT id FROM comments WHERE post_id = $1)`, postID)
		pool.Exec(ctx, `DELETE FROM comment_idempotency WHERE post_id = $1`, postID)
		pool.Exec(ctx, `DELETE FROM comments WHERE post_id = $1`, postID)
		pool.Exec(ctx, `DELETE FROM post_engagement_counts WHERE post_id = $1`, postID)
		pool.Exec(ctx, `DELETE FROM posts WHERE id = $1`, postID)
	})
	return postID
}

func TestReplyIsOpenToEveryViewer(t *testing.T) {
	pool := openCommentCountDB(t)
	store := postgres.New(pool)
	ctx := context.Background()
	owner, commenter, stranger := uuid.New(), uuid.New(), uuid.New()
	postID := seedReplyPost(t, pool, owner)

	top, _, err := store.CreateCommentIdempotent(ctx, postID, commenter, "top", "", "")
	if err != nil {
		t.Fatal(err)
	}

	// A viewer who is neither the post owner nor the comment author replies.
	r1, notify, err := store.CreateReply(ctx, top.ID, stranger, "first reply")
	if err != nil {
		t.Fatalf("a non-owner could not reply: %v (the post-owner rule is back)", err)
	}
	if notify != commenter {
		t.Fatalf("reply notifies %s, want the comment author %s", notify, commenter)
	}
	if r1.ParentID == nil || *r1.ParentID != top.ID || !r1.IsReply || r1.PostID != postID {
		t.Fatalf("reply shape: parent=%v is_reply=%v post=%s", r1.ParentID, r1.IsReply, r1.PostID)
	}
	if r1.Reactions == nil {
		t.Fatal("a fresh reply ships reactions: null; the wire contract is an empty array")
	}

	// A second reply on the same comment.
	r2, _, err := store.CreateReply(ctx, top.ID, owner, "second reply")
	if err != nil {
		t.Fatalf("a second reply on the same comment was refused: %v (the one-reply cap is back)", err)
	}

	// Replying to a REPLY attaches to the reply's parent; the notification
	// goes to the reply's author.
	r3, notify3, err := store.CreateReply(ctx, r2.ID, commenter, "@owner third")
	if err != nil {
		t.Fatalf("replying to a reply: %v", err)
	}
	if r3.ParentID == nil || *r3.ParentID != top.ID {
		t.Fatalf("a reply to a reply attached to %v, want the top-level parent %s", r3.ParentID, top.ID)
	}
	if notify3 != owner {
		t.Fatalf("reply-to-reply notifies %s, want the reply's author %s", notify3, owner)
	}

	var replyCount int
	if err := pool.QueryRow(ctx, `SELECT reply_count FROM comments WHERE id = $1`, top.ID).Scan(&replyCount); err != nil {
		t.Fatal(err)
	}
	if replyCount != 3 {
		t.Fatalf("parent reply_count=%d, want 3", replyCount)
	}

	// ListComments still carries the FIRST reply inline and the full count.
	page, _, err := store.ListComments(ctx, postID, nil, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].Reply == nil || page[0].Reply.ID != r1.ID || page[0].ReplyCount != 3 {
		t.Fatalf("ListComments: %d rows, inline reply=%v, reply_count=%d", len(page), page[0].Reply != nil, page[0].ReplyCount)
	}

	// A held / deleted target is not a reply target.
	if _, _, err := store.SoftDeleteComment(ctx, r1.ID, stranger); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreateReply(ctx, r1.ID, owner, "to a deleted reply"); err == nil || err.Error() != "COMMENT_NOT_FOUND" {
		t.Fatalf("replying to a deleted reply: %v, want COMMENT_NOT_FOUND", err)
	}
}

func TestGetRepliesPagesOldestFirstAndSkipsHidden(t *testing.T) {
	pool := openCommentCountDB(t)
	store := postgres.New(pool)
	ctx := context.Background()
	owner, commenter, moderator := uuid.New(), uuid.New(), uuid.New()
	postID := seedReplyPost(t, pool, owner)

	top, _, err := store.CreateCommentIdempotent(ctx, postID, commenter, "top", "", "")
	if err != nil {
		t.Fatal(err)
	}
	var ids []uuid.UUID
	for i, body := range []string{"r1", "r2", "r3", "r4", "r5"} {
		r, _, err := store.CreateReply(ctx, top.ID, owner, body)
		if err != nil {
			t.Fatalf("reply %d: %v", i, err)
		}
		ids = append(ids, r.ID)
	}
	// r2 hidden by a moderator, r4 deleted by its author: neither pages.
	if _, _, err := store.SetCommentModerationStatus(ctx, moderator, ids[1], "hidden"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.SoftDeleteComment(ctx, ids[3], owner); err != nil {
		t.Fatal(err)
	}

	first, cursor, err := store.GetReplies(ctx, top.ID, nil, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || first[0].ID != ids[0] || first[1].ID != ids[2] || cursor == "" {
		t.Fatalf("page 1: %d rows (%v), cursor=%q; want r1,r3 with a cursor", len(first), replyBodies(first), cursor)
	}
	second, cursor2, err := store.GetReplies(ctx, top.ID, nil, cursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || second[0].ID != ids[4] || cursor2 != "" {
		t.Fatalf("page 2: %d rows (%v), cursor=%q; want r5 alone and no cursor", len(second), replyBodies(second), cursor2)
	}

	// limit is clamped: 0 → 20, 1000 → 100.
	all, _, err := store.GetReplies(ctx, top.ID, nil, "", 0)
	if err != nil || len(all) != 3 {
		t.Fatalf("default limit: %v rows=%d want 3", err, len(all))
	}
	if all[0].ID != ids[0] || all[1].ID != ids[2] || all[2].ID != ids[4] {
		t.Fatalf("not oldest-first: %v", replyBodies(all))
	}
	// Cleanup for the audit row the moderation write left.
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM post_admin_audit WHERE target_id = $1`, ids[1]) })
}

func replyBodies(rs []postgres.Comment) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Body)
	}
	return out
}

func TestCommentReactionReplacesDeletesAndSummarises(t *testing.T) {
	pool := openCommentCountDB(t)
	store := postgres.New(pool)
	ctx := context.Background()
	owner, commenter := uuid.New(), uuid.New()
	postID := seedReplyPost(t, pool, owner)
	top, _, err := store.CreateCommentIdempotent(ctx, postID, commenter, "react to me", "", "")
	if err != nil {
		t.Fatal(err)
	}

	// Four emojis from six viewers: 🔥×3, 😂×2, ❤️×1 … then a fourth kind
	// so the top-3 cut and the (count desc, emoji asc) order are both seen.
	viewers := make([]uuid.UUID, 7)
	for i := range viewers {
		viewers[i] = uuid.New()
	}
	set := func(v uuid.UUID, e string) {
		t.Helper()
		if err := store.SetCommentReaction(ctx, top.ID, v, e); err != nil {
			t.Fatalf("set %s: %v", e, err)
		}
	}
	set(viewers[0], "🔥")
	set(viewers[1], "🔥")
	set(viewers[2], "🔥")
	set(viewers[3], "😂")
	set(viewers[4], "😂")
	set(viewers[5], "❤️")
	set(viewers[6], "👀")

	// A second emoji from the same viewer REPLACES the first.
	set(viewers[6], "❤️")
	sum, err := store.GetCommentReactionSummary(ctx, top.ID, &viewers[6])
	if err != nil {
		t.Fatal(err)
	}
	if sum.ReactionCount != 7 {
		t.Fatalf("a second emoji added a reaction instead of replacing: reaction_count=%d want 7", sum.ReactionCount)
	}
	if sum.ViewerReaction == nil || *sum.ViewerReaction != "❤️" {
		t.Fatalf("viewer_reaction=%v want ❤️ after the replace", sum.ViewerReaction)
	}
	// Now ❤️×2 and 😂×2 tie: emoji asc breaks it (❤️ U+2764 < 😂 U+1F602? no —
	// compare as strings: "❤️" (E2 9D A4 …) < "😂" (F0 9F 98 82)).
	want := []postgres.CommentReaction{{"🔥", 3}, {"❤️", 2}, {"😂", 2}}
	if len(sum.Reactions) != 3 {
		t.Fatalf("reactions carries %d emojis, want the top 3: %v", len(sum.Reactions), sum.Reactions)
	}
	for i, w := range want {
		if sum.Reactions[i] != w {
			t.Fatalf("reactions[%d]=%v want %v (order is count desc, emoji asc)", i, sum.Reactions[i], w)
		}
	}
	if len(sum.Reactions) > 0 && sum.Reactions[len(sum.Reactions)-1].Emoji == "👀" {
		t.Fatal("the fourth emoji is on the wire; only the top 3 should be")
	}

	// viewer_reaction is per viewer.
	other, err := store.GetCommentReactionSummary(ctx, top.ID, &viewers[0])
	if err != nil {
		t.Fatal(err)
	}
	if other.ViewerReaction == nil || *other.ViewerReaction != "🔥" {
		t.Fatalf("viewer 0 sees viewer_reaction=%v want 🔥", other.ViewerReaction)
	}
	anon, err := store.GetCommentReactionSummary(ctx, top.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if anon.ViewerReaction != nil {
		t.Fatalf("anonymous viewer_reaction=%v want null", *anon.ViewerReaction)
	}

	// The page carries the same figures, and like_count == reaction_count.
	page, _, err := store.ListComments(ctx, postID, &viewers[3], "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].ReactionCount != 7 || page[0].LikeCount != 7 || len(page[0].Reactions) != 3 ||
		page[0].ViewerReaction == nil || *page[0].ViewerReaction != "😂" {
		t.Fatalf("ListComments row: reaction_count=%d like_count=%d reactions=%v viewer=%v",
			page[0].ReactionCount, page[0].LikeCount, page[0].Reactions, page[0].ViewerReaction)
	}

	// Delete removes exactly the viewer's row; a second delete is a no-op.
	removed, err := store.RemoveCommentReaction(ctx, top.ID, viewers[6])
	if err != nil || !removed {
		t.Fatalf("remove: %v removed=%v", err, removed)
	}
	again, err := store.RemoveCommentReaction(ctx, top.ID, viewers[6])
	if err != nil || again {
		t.Fatalf("second remove: %v removed=%v (must be false)", err, again)
	}
	sum, err = store.GetCommentReactionSummary(ctx, top.ID, &viewers[6])
	if err != nil {
		t.Fatal(err)
	}
	if sum.ReactionCount != 6 || sum.ViewerReaction != nil {
		t.Fatalf("after delete: reaction_count=%d viewer=%v, want 6 and null", sum.ReactionCount, sum.ViewerReaction)
	}

	// A comment with no reactions has an empty array, not null.
	bare, _, err := store.CreateCommentIdempotent(ctx, postID, commenter, "bare", "", "")
	if err != nil {
		t.Fatal(err)
	}
	empty, err := store.GetCommentReactionSummary(ctx, bare.ID, nil)
	if err != nil || empty.Reactions == nil || empty.ReactionCount != 0 {
		t.Fatalf("no reactions: %v reactions=%v count=%d", err, empty.Reactions, empty.ReactionCount)
	}
}

func TestLegacyLikeTogglesOnTheReactionTable(t *testing.T) {
	pool := openCommentCountDB(t)
	store := postgres.New(pool)
	ctx := context.Background()
	owner, commenter, liker := uuid.New(), uuid.New(), uuid.New()
	postID := seedReplyPost(t, pool, owner)
	top, _, err := store.CreateCommentIdempotent(ctx, postID, commenter, "like me", "", "")
	if err != nil {
		t.Fatal(err)
	}

	// Like on: the viewer's row is ❤️ and the new-client summary sees it.
	liked, err := store.ToggleCommentHeart(ctx, top.ID, liker)
	if err != nil || !liked {
		t.Fatalf("like on: %v liked=%v", err, liked)
	}
	sum, _ := store.GetCommentReactionSummary(ctx, top.ID, &liker)
	if sum.ReactionCount != 1 || sum.ViewerReaction == nil || *sum.ViewerReaction != postgres.LikeEmoji {
		t.Fatalf("after like: count=%d viewer=%v", sum.ReactionCount, sum.ViewerReaction)
	}

	// Like again: off.
	liked, err = store.ToggleCommentHeart(ctx, top.ID, liker)
	if err != nil || liked {
		t.Fatalf("like off: %v liked=%v", err, liked)
	}
	sum, _ = store.GetCommentReactionSummary(ctx, top.ID, &liker)
	if sum.ReactionCount != 0 || sum.ViewerReaction != nil {
		t.Fatalf("after unlike: count=%d viewer=%v", sum.ReactionCount, sum.ViewerReaction)
	}

	// A viewer who reacted 🔥 through the new route then taps like: the
	// heart replaces the 🔥 (still one row), and a further like removes it.
	if err := store.SetCommentReaction(ctx, top.ID, liker, "🔥"); err != nil {
		t.Fatal(err)
	}
	liked, err = store.ToggleCommentHeart(ctx, top.ID, liker)
	if err != nil || !liked {
		t.Fatalf("like over 🔥: %v liked=%v", err, liked)
	}
	sum, _ = store.GetCommentReactionSummary(ctx, top.ID, &liker)
	if sum.ReactionCount != 1 || *sum.ViewerReaction != postgres.LikeEmoji {
		t.Fatalf("like over 🔥: count=%d viewer=%v, want 1 and ❤️", sum.ReactionCount, sum.ViewerReaction)
	}

	// The column is not what the wire reads any more: like_count on the
	// page equals the table's count even though the column was never bumped.
	var column int
	if err := pool.QueryRow(ctx, `SELECT like_count FROM comments WHERE id = $1`, top.ID).Scan(&column); err != nil {
		t.Fatal(err)
	}
	page, _, _ := store.ListComments(ctx, postID, nil, "", 20)
	if column != 0 || page[0].LikeCount != 1 {
		t.Fatalf("column=%d wire like_count=%d; the wire must read the reaction table (1), the column stays unmaintained (0)", column, page[0].LikeCount)
	}
}

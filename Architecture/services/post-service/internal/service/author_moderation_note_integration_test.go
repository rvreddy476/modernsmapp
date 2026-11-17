//go:build integration

package service

import (
	"context"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// The author's takedown reason on GET /v1/posts/:id, end to end on a real
// Postgres (POSTGRES_DSN, a *_test database):
//
//	POSTGRES_DSN=postgres://…/post_it_test?sslmode=disable go test -tags integration ./internal/service/ -run AuthorModerationNoteIT
//
// An upheld report takes the post down: the author's read carries
// moderation_reason and moderation_appealable; a stranger and a signed-out
// viewer get the 404 (nil detail) with nothing; the appeal restores the
// post and the fields are gone.
func TestAuthorModerationNoteIT_ReportTakedownThenAppeal(t *testing.T) {
	pool := openStandingDB(t)
	ctx := context.Background()
	svc, _, st := standingITService(t, pool, standingOKBody)
	author, stranger := uuid.New(), uuid.New()
	p := &postgres.Post{ID: uuid.New(), AuthorID: author, Text: "takedown reason proof",
		Visibility: "public", ContentType: "post", PostType: "post", AppOrigin: "postbook", ReviewStatus: "approved"}
	if err := st.CreatePostWithEvent(ctx, p, "", nil); err != nil {
		t.Fatal(err)
	}

	reportID := uuid.New()
	if _, err := svc.ModeratePost(ctx, postgres.ModeratePostInput{
		DecisionID: reportID, PostID: p.ID, ActorID: uuid.New(), Action: "reject",
		Reason: "Spam: repeated promotional links", Source: "report", SourceRefID: &reportID,
	}); err != nil {
		t.Fatalf("report takedown: %v", err)
	}

	mine, err := svc.GetPost(ctx, p.ID, &author)
	if err != nil || mine == nil {
		t.Fatalf("author read: %v %v", mine, err)
	}
	if mine.ModerationReason == nil || *mine.ModerationReason != "Spam: repeated promotional links" ||
		mine.ModerationAppealable == nil || !*mine.ModerationAppealable {
		t.Fatalf("author read: reason=%v appealable=%v", mine.ModerationReason, mine.ModerationAppealable)
	}
	if d, err := svc.GetPost(ctx, p.ID, &stranger); err != nil || d != nil {
		t.Fatalf("stranger read: %v %v, want the 404", d, err)
	}
	if d, err := svc.GetPost(ctx, p.ID, nil); err != nil || d != nil {
		t.Fatalf("signed-out read: %v %v, want the 404", d, err)
	}

	appealID := uuid.New()
	if _, err := svc.ModeratePost(ctx, postgres.ModeratePostInput{
		DecisionID: appealID, PostID: p.ID, ActorID: uuid.New(), Action: "approve",
		Reason: "Appeal upheld", Source: "appeal", SourceRefID: &appealID,
	}); err != nil {
		t.Fatalf("appeal: %v", err)
	}
	mine, err = svc.GetPost(ctx, p.ID, &author)
	if err != nil || mine == nil {
		t.Fatalf("author read after appeal: %v %v", mine, err)
	}
	if mine.ModerationReason != nil || mine.ModerationAppealable != nil {
		t.Fatal("the restored post still carries the takedown reason")
	}
	if d, err := svc.GetPost(ctx, p.ID, &stranger); err != nil || d == nil || d.ModerationReason != nil {
		t.Fatalf("stranger after appeal: %v %v", d, err)
	}
}

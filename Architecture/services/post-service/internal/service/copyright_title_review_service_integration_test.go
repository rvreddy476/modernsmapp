//go:build integration

package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	postEvents "github.com/atpost/post-service/internal/events"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Copyright title review end to end on a real Postgres (POSTGRES_DSN, a
// *_test database; openStandingDB refuses any other name):
//
//	POSTGRES_DSN=postgres://…/post_it_test?sslmode=disable go test -tags integration ./internal/service/ -run TitleReviewServiceIT
//
// A scheduled 1-hour long video naming a catalogue title goes live held:
// the direct read is 404 for everyone but its author, the author sees
// copyright_review pending, nothing was announced; the reviewer's clear
// makes it readable and writes exactly one PostCreated carrying
// eligible_from; a second identical upload blocked stays hidden.

func TestTitleReviewServiceIT_HoldClearBlock(t *testing.T) {
	pool := openStandingDB(t)
	ctx := context.Background()
	for _, ddl := range []string{
		`ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS width INT`,
		`ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS height INT`,
		`ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS alt_text TEXT`,
		`ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS alt_decorative BOOLEAN`,
		`ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS duration_ms INT`,
	} {
		if _, err := pool.Exec(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	svc, _, st := standingITService(t, pool, standingOKBody)
	svc.channels = st
	// The outbox rows are the assertion; the zero producer never dials.
	svc.producer = &postEvents.Producer{}
	author := uuid.New()
	if _, err := st.CreateCopyrightTitle(ctx, uuid.New(), postgres.CopyrightTitleWrite{Title: "Jailer 2", Active: true}, uuid.New()); err != nil {
		t.Fatal(err)
	}

	scheduledLongVideo := func() uuid.UUID {
		t.Helper()
		mediaID := uuid.New()
		if _, err := pool.Exec(ctx, `INSERT INTO media_assets (id, uploader_id, file_type, processing_status, moderation_status, duration_seconds)
			VALUES ($1,$2,'video','ready','passed',3600)`, mediaID, author); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-time.Minute).UTC()
		p := &postgres.Post{ID: uuid.New(), AuthorID: author, Text: "Watch it tonight", Title: "Jailer 2 full movie",
			Visibility: "public", ContentType: "long_video", PostType: "video", AppOrigin: "posttube",
			ReviewStatus: "approved", PublishAt: &at, Media: []postgres.PostMedia{{MediaID: mediaID, Kind: "video"}}}
		if err := st.CreatePostWithEvent(ctx, p, "", nil); err != nil {
			t.Fatal(err)
		}
		return p.ID
	}

	held := scheduledLongVideo()
	published, err := svc.PublishScheduled(ctx, held, &author, false)
	if err != nil || !published {
		t.Fatalf("publish: %v %v", published, err)
	}
	stranger := uuid.New()
	if d, err := svc.GetPost(ctx, held, &stranger); err != nil || d != nil {
		t.Fatalf("a stranger read the held video: %+v %v", d, err)
	}
	if d, err := svc.GetPost(ctx, held, nil); err != nil || d != nil {
		t.Fatalf("a signed-out viewer read the held video: %+v %v", d, err)
	}
	mine, err := svc.GetPost(ctx, held, &author)
	if err != nil || mine == nil {
		t.Fatalf("the author lost their video: %v", err)
	}
	svc.applyHubDetail(ctx, mine, &author)
	if mine.CopyrightReview == nil || mine.CopyrightReview.State != "pending" || mine.CopyrightReview.MatchedTitle != "Jailer 2" {
		t.Fatalf("author view: %+v", mine.CopyrightReview)
	}
	if n := outboxCount(t, pool, held, events.PostCreated); n != 0 {
		t.Fatalf("a held flip announced itself %d times", n)
	}

	reviews, _, err := st.ListTitleReviews(ctx, "pending", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	var reviewID uuid.UUID
	for _, r := range reviews {
		if r.PostID == held {
			reviewID = r.ID
		}
	}
	if reviewID == uuid.Nil {
		t.Fatal("the held video is not in the queue")
	}
	// (the clear writes the withheld PostCreated through the outbox)
	if _, err := svc.DecideCopyrightReview(ctx, reviewID, uuid.New(), "clear", "licensed"); err != nil {
		t.Fatal(err)
	}
	if d, err := svc.GetPost(ctx, held, &stranger); err != nil || d == nil {
		t.Fatalf("a cleared video is still hidden: %v", err)
	}
	if n := outboxCount(t, pool, held, events.PostCreated); n != 1 {
		t.Fatalf("the clear wrote PostCreated %d times, want once", n)
	}
	var created []byte
	if err := pool.QueryRow(ctx, `SELECT payload FROM post_outbox_events WHERE aggregate_id=$1 AND event_type=$2`, held, events.PostCreated).Scan(&created); err != nil {
		t.Fatal(err)
	}
	var pc events.PostCreatedPayload
	_ = json.Unmarshal(created, &pc)
	if pc.EligibleFrom == nil || pc.ReviewStatus != "approved" {
		t.Fatalf("the withheld PostCreated: eligible_from=%v review=%q", pc.EligibleFrom, pc.ReviewStatus)
	}
	if _, err := svc.DecideCopyrightReview(ctx, reviewID, uuid.New(), "clear", ""); err == nil {
		t.Fatal("a decided review was decided again")
	}
	if n := outboxCount(t, pool, held, events.PostCreated); n != 1 {
		t.Fatalf("a second clear fanned out again (%d)", n)
	}

	blocked := scheduledLongVideo()
	if _, err := svc.PublishScheduled(ctx, blocked, &author, false); err != nil {
		t.Fatal(err)
	}
	reviews, _, _ = st.ListTitleReviews(ctx, "pending", "", 50)
	for _, r := range reviews {
		if r.PostID == blocked {
			if _, err := svc.DecideCopyrightReview(ctx, r.ID, uuid.New(), "block", "pirated"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if d, _ := svc.GetPost(ctx, blocked, &stranger); d != nil {
		t.Fatal("a blocked video is readable")
	}
	var payload []byte
	if err := pool.QueryRow(ctx, `SELECT payload FROM post_outbox_events WHERE aggregate_id=$1 AND event_type=$2 ORDER BY created_at DESC LIMIT 1`,
		blocked, events.EventPostCopyrightReview).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var ev events.PostCopyrightReviewPayload
	_ = json.Unmarshal(payload, &ev)
	if ev.State != events.CopyrightReviewBlocked || ev.AuthorID != author.String() || ev.DecidedAt == nil {
		t.Fatalf("block event: %+v", ev)
	}
}

func outboxCount(t *testing.T, pool *pgxpool.Pool, postID uuid.UUID, eventType string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM post_outbox_events WHERE aggregate_id=$1 AND event_type=$2`, postID, eventType).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

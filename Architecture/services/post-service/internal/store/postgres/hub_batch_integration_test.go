//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Creator Hub batch against a real Postgres (2026-09-28; migrations 052
// and 053), on the same _test-only rig as the MTube suite:
//
//	POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/store/postgres/ -run HubBatch -v

func auditRows(t *testing.T, r *mtubeRig, postID uuid.UUID, action string) []string {
	t.Helper()
	rows, err := r.pool.Query(context.Background(),
		`SELECT changes::text FROM post_edit_audit WHERE post_id = $1 AND action = $2 ORDER BY created_at`, postID, action)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func TestHubBatchEditWritesEverySettingAndAuditsIt(t *testing.T) {
	r := newMTubeRig(t)
	ctx := context.Background()
	post := r.newPost(t, r.owner, "long_video", "public", "podcasts")
	related := r.newPost(t, r.owner, "long_video", "public", "podcasts")

	// Defaults from migration 052.
	got, err := r.store.GetPost(ctx, post.ID)
	if err != nil || got.AgeRestricted || got.HideLikeCount || got.DefaultCommentSort != "top" || got.RelatedPostID != nil {
		t.Fatalf("defaults: %+v %v", got, err)
	}

	yes, no := true, false
	license, remix, mod, access, sort, loc := "creative_commons", "disallow", "strict", "nobody", "newest", "Hyderabad"
	day := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	rid := related.ID
	updated, err := r.store.UpdatePostFields(ctx, post.ID, r.owner, postgres.PostEditPatch{
		PaidPromotion: &yes, AlteredContent: &yes, License: &license, AllowEmbedding: &no,
		RecordingDate: &day, RecordingLocation: &loc, RemixSetting: &remix, CommentModeration: &mod,
		CommentAccess: &access, AgeRestricted: &yes, HideLikeCount: &yes, DefaultCommentSort: &sort, RelatedPostID: &rid,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.PaidPromotion || !updated.AlteredContent || updated.License != license || updated.AllowEmbedding ||
		updated.RecordingDate == nil || updated.RecordingDate.Format("2006-01-02") != "2026-09-20" || updated.RecordingLocation != loc ||
		updated.RemixSetting != remix || updated.CommentModeration != mod || updated.CommentAccess != access ||
		!updated.AgeRestricted || !updated.HideLikeCount || updated.DefaultCommentSort != sort ||
		updated.RelatedPostID == nil || *updated.RelatedPostID != rid {
		t.Fatalf("not written: %+v", updated)
	}
	audits := auditRows(t, r, post.ID, "post.edit")
	if len(audits) == 0 {
		t.Fatal("no audit row")
	}
	last := audits[len(audits)-1]
	for _, key := range []string{`"age_restricted"`, `"hide_like_count"`, `"default_comment_sort"`, `"related_post_id"`, `"recording_date"`, `"license"`} {
		if !strings.Contains(last, key) {
			t.Fatalf("audit %s lacks %s", last, key)
		}
	}
	// The access-state revalidation (cache hits) sees the gate columns.
	state, err := r.store.GetPostAccessState(ctx, post.ID)
	if err != nil || state == nil || !state.AgeRestricted || state.Visibility != "public" {
		t.Fatalf("access state: %+v %v", state, err)
	}

	// "" on the wire clears the two nullable columns.
	if updated, err = r.store.UpdatePostFields(ctx, post.ID, r.owner, postgres.PostEditPatch{ClearRecordingDate: true, ClearRelatedPost: true}); err != nil {
		t.Fatal(err)
	}
	if updated.RecordingDate != nil || updated.RelatedPostID != nil {
		t.Fatalf("clear: date=%v related=%v", updated.RecordingDate, updated.RelatedPostID)
	}

	// The database refuses what the service would: an unknown sort and a
	// post that points at itself.
	if _, err := r.pool.Exec(ctx, `UPDATE posts SET default_comment_sort = 'oldest' WHERE id = $1`, post.ID); err == nil {
		t.Fatal("default_comment_sort CHECK missing")
	}
	if _, err := r.pool.Exec(ctx, `UPDATE posts SET related_post_id = id WHERE id = $1`, post.ID); err == nil {
		t.Fatal("related_post_id self CHECK missing")
	}
	// A hard delete of the related post clears the link (FK SET NULL).
	if _, err := r.store.UpdatePostFields(ctx, post.ID, r.owner, postgres.PostEditPatch{RelatedPostID: &rid}); err != nil {
		t.Fatal(err)
	}
	if err := hardDeletePost(ctx, r, related.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.store.GetPost(ctx, post.ID); got.RelatedPostID != nil {
		t.Fatalf("related_post_id survived the hard delete: %v", got.RelatedPostID)
	}

	// The bulk route's audit action is accepted by the widened CHECK.
	if _, err := r.store.UpdatePostFields(ctx, post.ID, r.owner, postgres.PostEditPatch{HideLikeCount: &no, AuditAction: "post.bulk_edit"}); err != nil {
		t.Fatalf("bulk_edit audit: %v", err)
	}
	if len(auditRows(t, r, post.ID, "post.bulk_edit")) != 1 {
		t.Fatal("bulk edit not audited as post.bulk_edit")
	}
}

// notify_subscribers: the policy, the rev bump and the outbox event commit
// with the edit.
func TestHubBatchEditWritesDistributionWithRevAndEvent(t *testing.T) {
	r := newMTubeRig(t)
	ctx := context.Background()
	post := r.newPost(t, r.owner, "long_video", "private", "podcasts")
	before, _ := r.store.GetPost(ctx, post.ID)
	policy := json.RawMessage(`{"version":1,"main_feed":true,"notify_subscribers":false}`)
	updated, err := r.store.UpdatePostFields(ctx, post.ID, r.owner, postgres.PostEditPatch{
		Distribution:       policy,
		DistributionChange: &postgres.PostEditChange{From: true, To: false},
		DistributionEvent: func(rev int64) (string, interface{}) {
			return events.PostDistributionUpdated, events.PostDistributionUpdatedPayload{PostID: post.ID.String(), DistributionRev: rev}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.DistributionRev != before.DistributionRev+1 || !strings.Contains(string(updated.Distribution), `"notify_subscribers": false`) &&
		!strings.Contains(string(updated.Distribution), `"notify_subscribers":false`) {
		t.Fatalf("policy / rev: %s rev=%d (was %d)", updated.Distribution, updated.DistributionRev, before.DistributionRev)
	}
	var payload string
	if err := r.pool.QueryRow(ctx, `SELECT payload::text FROM post_outbox_events WHERE aggregate_id = $1 AND event_type = $2 ORDER BY created_at DESC LIMIT 1`,
		post.ID, events.PostDistributionUpdated).Scan(&payload); err != nil {
		t.Fatalf("no PostDistributionUpdated in the outbox: %v", err)
	}
	if !strings.Contains(payload, `"distribution_rev":`) {
		t.Fatalf("payload: %s", payload)
	}
	if a := auditRows(t, r, post.ID, "post.edit"); len(a) == 0 || !strings.Contains(a[len(a)-1], `"notify_subscribers"`) {
		t.Fatalf("audit: %v", a)
	}
}

func TestHubBatchPrivateSharesReplaceAuditAndCascade(t *testing.T) {
	r := newMTubeRig(t)
	ctx := context.Background()
	post := r.newPost(t, r.owner, "long_video", "private", "podcasts")
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{a, b, c} {
		newUser(t, r.pool, id)
	}

	rows, err := r.store.ReplacePrivateShares(ctx, post.ID, r.owner, []uuid.UUID{a, b})
	if err != nil || len(rows) != 2 {
		t.Fatalf("first replace: %+v %v", rows, err)
	}
	firstAdded := map[uuid.UUID]time.Time{}
	for _, row := range rows {
		firstAdded[row.UserID] = row.AddedAt
	}
	time.Sleep(10 * time.Millisecond)
	rows, err = r.store.ReplacePrivateShares(ctx, post.ID, r.owner, []uuid.UUID{b, c})
	if err != nil || len(rows) != 2 {
		t.Fatalf("second replace: %+v %v", rows, err)
	}
	for _, row := range rows {
		if row.UserID == a {
			t.Fatal("a was not removed")
		}
		if row.UserID == b && !row.AddedAt.Equal(firstAdded[b]) {
			t.Fatal("b lost its added_at on a replace that kept it")
		}
	}
	audits := auditRows(t, r, post.ID, "post.private_shares")
	if len(audits) != 2 || !strings.Contains(audits[1], a.String()) || !strings.Contains(audits[1], c.String()) {
		t.Fatalf("audits: %v", audits)
	}
	// The same list again writes no audit row.
	if _, err := r.store.ReplacePrivateShares(ctx, post.ID, r.owner, []uuid.UUID{c, b}); err != nil {
		t.Fatal(err)
	}
	if n := len(auditRows(t, r, post.ID, "post.private_shares")); n != 2 {
		t.Fatalf("no-op replace audited (%d rows)", n)
	}

	// Lookups are per viewer and per post.
	other := r.newPost(t, r.owner, "long_video", "private", "podcasts")
	shared, err := r.store.PrivateSharedPostIDs(ctx, b, []uuid.UUID{post.ID, other.ID})
	if err != nil || !shared[post.ID] || shared[other.ID] {
		t.Fatalf("shared ids for b: %v %v", shared, err)
	}
	if shared, _ := r.store.PrivateSharedPostIDs(ctx, a, []uuid.UUID{post.ID}); shared[post.ID] {
		t.Fatal("a removed user still reads as shared")
	}

	// Store guards: not the owner, missing post, unknown user (users FK).
	if _, err := r.store.ReplacePrivateShares(ctx, post.ID, b, []uuid.UUID{c}); !errors.Is(err, postgres.ErrPostEditNotOwned) {
		t.Fatalf("stranger: %v", err)
	}
	if _, err := r.store.ReplacePrivateShares(ctx, uuid.New(), r.owner, []uuid.UUID{c}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("missing: %v", err)
	}
	if _, err := r.store.ReplacePrivateShares(ctx, post.ID, r.owner, []uuid.UUID{uuid.New()}); !errors.Is(err, postgres.ErrPrivateShareUnknownUser) {
		t.Fatalf("unknown user: %v", err)
	}
	// Empty list clears.
	if rows, err := r.store.ReplacePrivateShares(ctx, post.ID, r.owner, []uuid.UUID{}); err != nil || len(rows) != 0 {
		t.Fatalf("clear: %+v %v", rows, err)
	}
	// The rows go with the post.
	if _, err := r.store.ReplacePrivateShares(ctx, other.ID, r.owner, []uuid.UUID{a}); err != nil {
		t.Fatal(err)
	}
	if err := hardDeletePost(ctx, r, other.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM post_private_shares WHERE post_id = $1`, other.ID).Scan(&n)
	if n != 0 {
		t.Fatalf("%d share rows outlived their post", n)
	}
}

// hardDeletePost removes a post row the way the purge does for the parts
// these tests create (the engagement-counts row has no cascade).
func hardDeletePost(ctx context.Context, r *mtubeRig, id uuid.UUID) error {
	if _, err := r.pool.Exec(ctx, `DELETE FROM post_engagement_counts WHERE post_id = $1`, id); err != nil {
		return err
	}
	_, err := r.pool.Exec(ctx, `DELETE FROM posts WHERE id = $1`, id)
	return err
}

// Migrations 052 and 053 are idempotent: the runner applied them at
// bootstrap, and a second run of the same bytes (and setup.sql, which every
// boot replays) changes nothing and fails nothing.
func TestHubBatchMigrationsAreIdempotent(t *testing.T) {
	r := newMTubeRig(t)
	for _, name := range []string{"052_hub_settings.sql", "053_post_private_shares.sql"} {
		sql, err := migrationSQL(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.pool.Exec(context.Background(), sql); err != nil {
			t.Fatalf("re-run %s: %v", name, err)
		}
	}
}

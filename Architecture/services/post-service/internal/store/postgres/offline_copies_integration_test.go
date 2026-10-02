//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Offline copies against a real Postgres (2026-10-02; migration 060).
//
//	POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/store/postgres/ -run OfflineIT -v
//
// Same _test-only rig as the MTube suite (setup.sql + every migration), so
// migration 060 itself is under test: the table, its CHECK, its cascade from
// posts, and the two places a post change revokes copies in its own
// transaction (the soft delete, and the owner edit).

const (
	offDeviceA = "device-web-0001"
	offDeviceB = "device-phone-0002"
)

type offlineITRig struct {
	*mtubeRig
	now time.Time
}

func newOfflineITRig(t *testing.T) *offlineITRig {
	t.Helper()
	return &offlineITRig{mtubeRig: newMTubeRig(t), now: time.Now().UTC().Truncate(time.Microsecond)}
}

func (r *offlineITRig) video(t *testing.T) *postgres.Post {
	t.Helper()
	p := r.newPost(t, r.owner, "long_video", "public", "podcasts")
	t.Cleanup(func() {
		_, _ = r.pool.Exec(context.Background(), `DELETE FROM post_edit_audit WHERE post_id = $1`, p.ID)
	})
	return p
}

func (r *offlineITRig) grant(t *testing.T, user, post uuid.UUID, device string, limit int) (*postgres.OfflineCopy, bool, error) {
	t.Helper()
	return r.store.GrantOfflineCopy(context.Background(), user, post, device, r.now, r.now.Add(30*24*time.Hour), limit, json.RawMessage(`{"media":{"variant":"720p"}}`))
}

func (r *offlineITRig) mustGrant(t *testing.T, user, post uuid.UUID, device string) *postgres.OfflineCopy {
	t.Helper()
	row, _, err := r.grant(t, user, post, device, 100)
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	return row
}

// state reads one row straight from the table.
func (r *offlineITRig) state(t *testing.T, user, post uuid.UUID, device string) (revoked bool, reason string, expires time.Time, found bool) {
	t.Helper()
	var revokedAt *time.Time
	var why *string
	err := r.pool.QueryRow(context.Background(),
		`SELECT revoked_at, revoke_reason, expires_at FROM post_offline_copies WHERE user_id=$1 AND post_id=$2 AND device_id=$3`,
		user, post, device).Scan(&revokedAt, &why, &expires)
	if err != nil {
		return false, "", time.Time{}, false
	}
	if why != nil {
		reason = *why
	}
	return revokedAt != nil, reason, expires, true
}

func TestOfflineITGrantRefreshAndStoredCard(t *testing.T) {
	r := newOfflineITRig(t)
	post, viewer := r.video(t), uuid.New()

	row, created, err := r.grant(t, viewer, post.ID, offDeviceA, 100)
	if err != nil || !created {
		t.Fatalf("grant: created=%v err=%v", created, err)
	}
	if !row.GrantedAt.Equal(r.now) || !row.ExpiresAt.Equal(r.now.Add(30*24*time.Hour)) || row.RevokedAt != nil ||
		row.LastCheckedAt == nil || !strings.Contains(string(row.Card), `"720p"`) {
		t.Fatalf("row = %+v card=%s", row, row.Card)
	}

	// Same (user, post, device) again: the same copy, refreshed.
	first := r.now
	r.now = r.now.Add(5 * 24 * time.Hour)
	again, created, err := r.grant(t, viewer, post.ID, offDeviceA, 100)
	if err != nil || created {
		t.Fatalf("refresh: created=%v err=%v", created, err)
	}
	if !again.GrantedAt.Equal(first) || !again.ExpiresAt.Equal(r.now.Add(30*24*time.Hour)) {
		t.Fatalf("refresh: granted_at=%v expires_at=%v", again.GrantedAt, again.ExpiresAt)
	}
	var n int
	_ = r.pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM post_offline_copies WHERE user_id=$1`, viewer).Scan(&n)
	if n != 1 {
		t.Fatalf("rows = %d, want one per (user, post, device)", n)
	}

	// A revoked copy granted again is a new copy: revoke cleared, new granted_at.
	if _, err := r.store.RevokeOfflineCopies(context.Background(), viewer, offDeviceA, []uuid.UUID{post.ID}, postgres.OfflineRevokeRemoved, r.now); err != nil {
		t.Fatal(err)
	}
	r.now = r.now.Add(time.Hour)
	revived, created, err := r.grant(t, viewer, post.ID, offDeviceA, 100)
	if err != nil || !created || revived.RevokedAt != nil || revived.RevokeReason != "" || !revived.GrantedAt.Equal(r.now) {
		t.Fatalf("revive: created=%v err=%v row=%+v", created, err, revived)
	}
}

func TestOfflineITLimitCountsActiveCopiesPerUser(t *testing.T) {
	r := newOfflineITRig(t)
	viewer, other := uuid.New(), uuid.New()
	const limit = 3
	posts := []*postgres.Post{r.video(t), r.video(t), r.video(t), r.video(t)}

	r.mustGrant(t, viewer, posts[0].ID, offDeviceA)
	r.mustGrant(t, viewer, posts[1].ID, offDeviceB) // another device, the same user's count
	r.mustGrant(t, viewer, posts[2].ID, offDeviceA)

	if _, _, err := r.grant(t, viewer, posts[3].ID, offDeviceA, limit); !errors.Is(err, postgres.ErrOfflineCopyLimit) {
		t.Fatalf("4th copy at limit 3: %v", err)
	}
	if _, _, _, found := r.state(t, viewer, posts[3].ID, offDeviceA); found {
		t.Fatal("the refused grant wrote a row")
	}
	// The same post on another device is another copy.
	if _, _, err := r.grant(t, viewer, posts[0].ID, offDeviceB, limit); !errors.Is(err, postgres.ErrOfflineCopyLimit) {
		t.Fatalf("same post, other device, at the limit: %v", err)
	}
	// Refreshing a copy already held is not a new one.
	if _, created, err := r.grant(t, viewer, posts[0].ID, offDeviceA, limit); err != nil || created {
		t.Fatalf("refresh at the limit: created=%v err=%v", created, err)
	}
	// Another user has their own count.
	if _, _, err := r.grant(t, other, posts[3].ID, offDeviceA, limit); err != nil {
		t.Fatalf("another user: %v", err)
	}
	// A revoked copy frees a slot.
	if _, err := r.store.RevokeOfflineCopies(context.Background(), viewer, offDeviceB, []uuid.UUID{posts[1].ID}, postgres.OfflineRevokeRemoved, r.now); err != nil {
		t.Fatal(err)
	}
	if _, created, err := r.grant(t, viewer, posts[3].ID, offDeviceA, limit); err != nil || !created {
		t.Fatalf("after a revoke: created=%v err=%v", created, err)
	}
	// So does an expired one.
	if _, err := r.pool.Exec(context.Background(), `UPDATE post_offline_copies SET expires_at = $3 WHERE user_id=$1 AND post_id=$2`,
		viewer, posts[2].ID, r.now.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, created, err := r.grant(t, viewer, posts[1].ID, offDeviceB, limit); err != nil || !created {
		t.Fatalf("after an expiry: created=%v err=%v", created, err)
	}
}

// Two grants racing for the last slot: the count and the write are one
// transaction under a per-user lock, so exactly one of them takes it.
func TestOfflineITLimitHoldsUnderConcurrency(t *testing.T) {
	r := newOfflineITRig(t)
	viewer := uuid.New()
	const limit, racers = 2, 8
	r.mustGrant(t, viewer, r.video(t).ID, offDeviceA)
	posts := make([]uuid.UUID, racers)
	for i := range posts {
		posts[i] = r.video(t).ID
	}
	// Open the pool's connections first and hold the racers at a barrier:
	// otherwise the first grant finishes on the one warm connection while
	// the others are still connecting, and there is no race to lose.
	warm := int(r.pool.Config().MaxConns)
	if warm > racers {
		warm = racers
	}
	if warm < 2 {
		t.Fatalf("pool of %d connection(s) cannot race", warm)
	}
	conns := make([]*pgxpool.Conn, 0, warm)
	for i := 0; i < warm; i++ {
		c, err := r.pool.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
	}
	for _, c := range conns {
		c.Release()
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	granted, refused := 0, 0
	for _, id := range posts {
		wg.Add(1)
		go func(id uuid.UUID) {
			defer wg.Done()
			<-start
			_, _, err := r.store.GrantOfflineCopy(context.Background(), viewer, id, offDeviceA, r.now, r.now.Add(time.Hour), limit, nil)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				granted++
			case errors.Is(err, postgres.ErrOfflineCopyLimit):
				refused++
			default:
				t.Errorf("grant: %v", err)
			}
		}(id)
	}
	close(start)
	wg.Wait()
	var active int
	_ = r.pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM post_offline_copies WHERE user_id=$1 AND revoked_at IS NULL`, viewer).Scan(&active)
	if granted != 1 || refused != racers-1 || active != limit {
		t.Fatalf("granted=%d refused=%d active=%d, want 1, %d, %d", granted, refused, active, racers-1, limit)
	}
}

func TestOfflineITReadsTouchAndRevoke(t *testing.T) {
	r := newOfflineITRig(t)
	ctx := context.Background()
	viewer, other := uuid.New(), uuid.New()
	a, b, c := r.video(t), r.video(t), r.video(t)
	r.mustGrant(t, viewer, a.ID, offDeviceA)
	r.now = r.now.Add(time.Minute)
	r.mustGrant(t, viewer, b.ID, offDeviceA)
	r.mustGrant(t, viewer, c.ID, offDeviceB)
	r.mustGrant(t, other, a.ID, offDeviceA)

	rows, err := r.store.OfflineCopiesForPosts(ctx, viewer, offDeviceA, []uuid.UUID{a.ID, b.ID, c.ID, uuid.New()})
	if err != nil || len(rows) != 2 || rows[a.ID].PostID != a.ID || rows[b.ID].UserID != viewer {
		t.Fatalf("rows = %+v err=%v (want a and b on device A only)", rows, err)
	}
	list, err := r.store.ListActiveOfflineCopies(ctx, viewer, offDeviceA, r.now)
	if err != nil || len(list) != 2 || list[0].PostID != b.ID || list[1].PostID != a.ID {
		t.Fatalf("list = %+v err=%v (want b then a: newest grant first)", list, err)
	}

	// A check stamps last_checked_at and never moves expires_at.
	_, _, before, _ := r.state(t, viewer, a.ID, offDeviceA)
	checked := r.now.Add(48 * time.Hour)
	if err := r.store.TouchOfflineCopies(ctx, viewer, offDeviceA, []uuid.UUID{a.ID}, checked); err != nil {
		t.Fatal(err)
	}
	_, _, after, _ := r.state(t, viewer, a.ID, offDeviceA)
	var last time.Time
	_ = r.pool.QueryRow(ctx, `SELECT last_checked_at FROM post_offline_copies WHERE user_id=$1 AND post_id=$2 AND device_id=$3`, viewer, a.ID, offDeviceA).Scan(&last)
	if !after.Equal(before) || !last.Equal(checked) {
		t.Fatalf("touch: expires %v -> %v, last_checked_at=%v want %v", before, after, last, checked)
	}

	// Revoke: the row stays, the first reason stays, and it is idempotent.
	n, err := r.store.RevokeOfflineCopies(ctx, viewer, offDeviceA, []uuid.UUID{a.ID}, postgres.OfflineRevokeRemoved, r.now)
	if err != nil || n != 1 {
		t.Fatalf("revoke: n=%d err=%v", n, err)
	}
	n, err = r.store.RevokeOfflineCopies(ctx, viewer, offDeviceA, []uuid.UUID{a.ID, uuid.New()}, postgres.OfflineRevokePrivate, r.now.Add(time.Hour))
	if err != nil || n != 0 {
		t.Fatalf("second revoke: n=%d err=%v", n, err)
	}
	revoked, reason, _, found := r.state(t, viewer, a.ID, offDeviceA)
	if !found || !revoked || reason != "removed" {
		t.Fatalf("row after revoke: found=%v revoked=%v reason=%q", found, revoked, reason)
	}
	// It left the active list, stayed readable by id, and nobody else's moved.
	list, _ = r.store.ListActiveOfflineCopies(ctx, viewer, offDeviceA, r.now)
	rows, _ = r.store.OfflineCopiesForPosts(ctx, viewer, offDeviceA, []uuid.UUID{a.ID})
	if len(list) != 1 || list[0].PostID != b.ID || rows[a.ID].RevokedAt == nil {
		t.Fatalf("after revoke: list=%+v row=%+v", list, rows[a.ID])
	}
	if revoked, _, _, _ := r.state(t, other, a.ID, offDeviceA); revoked {
		t.Fatal("another user's copy was revoked")
	}
	if revoked, _, _, _ := r.state(t, viewer, c.ID, offDeviceB); revoked {
		t.Fatal("another device's copy was revoked")
	}
	// An expired copy is not active.
	if list, _ := r.store.ListActiveOfflineCopies(ctx, viewer, offDeviceA, r.now.Add(31*24*time.Hour)); len(list) != 0 {
		t.Fatalf("expired copies listed: %+v", list)
	}
}

func TestOfflineITDeviceIDIsHeldToSixtyFourCharacters(t *testing.T) {
	r := newOfflineITRig(t)
	post, viewer := r.video(t), uuid.New()
	if _, _, err := r.grant(t, viewer, post.ID, strings.Repeat("d", 65), 100); err == nil {
		t.Fatal("a 65-character device id was stored")
	}
	if _, _, err := r.grant(t, viewer, post.ID, "", 100); err == nil {
		t.Fatal("an empty device id was stored")
	}
	if _, _, err := r.grant(t, viewer, post.ID, strings.Repeat("d", 64), 100); err != nil {
		t.Fatalf("a 64-character device id was refused: %v", err)
	}
}

// Deleting the post revokes every copy of it, the owner's own included, in
// the delete's transaction; the purge worker's hard delete takes the rows.
func TestOfflineITDeletingThePostRevokesEveryCopy(t *testing.T) {
	r := newOfflineITRig(t)
	ctx := context.Background()
	post, kept := r.video(t), r.video(t)
	viewer := uuid.New()
	r.mustGrant(t, viewer, post.ID, offDeviceA)
	r.mustGrant(t, viewer, post.ID, offDeviceB)
	r.mustGrant(t, r.owner, post.ID, offDeviceA)
	r.mustGrant(t, viewer, kept.ID, offDeviceA)

	if _, err := r.store.DeleteUploadCascade(ctx, post.ID, r.owner, 30*24*time.Hour); err != nil {
		t.Fatalf("delete: %v", err)
	}
	for _, c := range []struct {
		user   uuid.UUID
		device string
	}{{viewer, offDeviceA}, {viewer, offDeviceB}, {r.owner, offDeviceA}} {
		revoked, reason, _, found := r.state(t, c.user, post.ID, c.device)
		if !found || !revoked || reason != "deleted" {
			t.Fatalf("copy of %v on %s: found=%v revoked=%v reason=%q", c.user, c.device, found, revoked, reason)
		}
	}
	if revoked, _, _, _ := r.state(t, viewer, kept.ID, offDeviceA); revoked {
		t.Fatal("a copy of another post was revoked")
	}
	// Restoring the post does not hand the copies back.
	if _, err := r.store.RestorePost(ctx, post.ID, r.owner, 30*24*time.Hour); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if revoked, _, _, _ := r.state(t, viewer, post.ID, offDeviceA); !revoked {
		t.Fatal("a restore un-revoked a copy")
	}
	// The hard delete takes the rows with the post.
	for _, q := range []string{
		`DELETE FROM post_outbox_events WHERE aggregate_id = $1`,
		`DELETE FROM post_engagement_counts WHERE post_id = $1`,
	} {
		if _, err := r.pool.Exec(ctx, q, post.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.pool.Exec(ctx, `DELETE FROM posts WHERE id = $1`, post.ID); err != nil {
		t.Fatalf("hard delete: %v", err)
	}
	var n int
	_ = r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM post_offline_copies WHERE post_id = $1`, post.ID).Scan(&n)
	if n != 0 {
		t.Fatalf("%d copies survived the post", n)
	}
}

// Turning downloads off, and making the post private, revoke in the edit's
// own transaction. The owner's copies stay; so do the copies of the users a
// private post is shared with.
func TestOfflineITOwnerEditRevokesCopies(t *testing.T) {
	r := newOfflineITRig(t)
	ctx := context.Background()
	yes, no := true, false

	t.Run("downloads turned off", func(t *testing.T) {
		post, viewer := r.video(t), uuid.New()
		if _, err := r.store.UpdatePostFields(ctx, post.ID, r.owner, postgres.PostEditPatch{AllowDownload: &yes}); err != nil {
			t.Fatal(err)
		}
		r.mustGrant(t, viewer, post.ID, offDeviceA)
		r.mustGrant(t, r.owner, post.ID, offDeviceA)

		// An edit that does not touch the switch, and one that sets it to
		// what it already is, revoke nothing.
		title := "Renamed"
		if _, err := r.store.UpdatePostFields(ctx, post.ID, r.owner, postgres.PostEditPatch{Title: &title, AllowDownload: &yes}); err != nil {
			t.Fatal(err)
		}
		if revoked, _, _, _ := r.state(t, viewer, post.ID, offDeviceA); revoked {
			t.Fatal("an unrelated edit revoked a copy")
		}

		if _, err := r.store.UpdatePostFields(ctx, post.ID, r.owner, postgres.PostEditPatch{AllowDownload: &no}); err != nil {
			t.Fatal(err)
		}
		if revoked, reason, _, _ := r.state(t, viewer, post.ID, offDeviceA); !revoked || reason != "not_allowed" {
			t.Fatalf("viewer copy: revoked=%v reason=%q", revoked, reason)
		}
		if revoked, _, _, _ := r.state(t, r.owner, post.ID, offDeviceA); revoked {
			t.Fatal("the owner's own copy was revoked")
		}
		// Turning it back on does not hand the copy back.
		if _, err := r.store.UpdatePostFields(ctx, post.ID, r.owner, postgres.PostEditPatch{AllowDownload: &yes}); err != nil {
			t.Fatal(err)
		}
		if revoked, _, _, _ := r.state(t, viewer, post.ID, offDeviceA); !revoked {
			t.Fatal("turning downloads back on un-revoked a copy")
		}
	})

	t.Run("made private", func(t *testing.T) {
		post := r.video(t)
		stranger, shared := uuid.New(), uuid.New()
		newUser(t, r.pool, shared)
		r.mustGrant(t, stranger, post.ID, offDeviceA)
		r.mustGrant(t, shared, post.ID, offDeviceA)
		r.mustGrant(t, r.owner, post.ID, offDeviceA)

		// Public -> unlisted is not a revocation.
		unlisted := "unlisted"
		if _, err := r.store.UpdatePostFields(ctx, post.ID, r.owner, postgres.PostEditPatch{Visibility: &unlisted}); err != nil {
			t.Fatal(err)
		}
		if revoked, _, _, _ := r.state(t, stranger, post.ID, offDeviceA); revoked {
			t.Fatal("unlisting revoked a copy")
		}

		if _, err := r.store.ReplacePrivateShares(ctx, post.ID, r.owner, []uuid.UUID{shared}); err != nil {
			t.Fatalf("share: %v", err)
		}
		private := "private"
		if _, err := r.store.UpdatePostFields(ctx, post.ID, r.owner, postgres.PostEditPatch{Visibility: &private}); err != nil {
			t.Fatal(err)
		}
		if revoked, reason, _, _ := r.state(t, stranger, post.ID, offDeviceA); !revoked || reason != "private" {
			t.Fatalf("stranger copy: revoked=%v reason=%q", revoked, reason)
		}
		if revoked, _, _, _ := r.state(t, shared, post.ID, offDeviceA); revoked {
			t.Fatal("the copy of a user the post is shared with was revoked")
		}
		if revoked, _, _, _ := r.state(t, r.owner, post.ID, offDeviceA); revoked {
			t.Fatal("the owner's own copy was revoked")
		}
	})
}

// Migration 060 is idempotent: re-applying it over a populated table keeps
// the rows.
func TestOfflineITMigrationIsIdempotent(t *testing.T) {
	r := newOfflineITRig(t)
	ctx := context.Background()
	post, viewer := r.video(t), uuid.New()
	r.mustGrant(t, viewer, post.ID, offDeviceA)
	sql, err := migrationSQL("060_post_offline_copies.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := r.pool.Exec(ctx, sql); err != nil {
			t.Fatalf("re-apply %d: %v", i, err)
		}
	}
	if _, _, _, found := r.state(t, viewer, post.ID, offDeviceA); !found {
		t.Fatal("re-applying the migration lost a row")
	}
}

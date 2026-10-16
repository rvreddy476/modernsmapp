//go:build integration

// The purge worker's store half against case-specific restrictions, on the
// real schema (migration 065). M7_POSTGRES_DSN must name a database whose
// name ends in _test (openRestrictionDB refuses anything else):
//
//	M7_POSTGRES_DSN=postgres://…/post_purge_it_test go test -tags integration ./internal/store/postgres/ -run PurgeHoldIT -v
//
// Dev's failure (2026-10-17): a post whose copyright hold had been placed
// and released failed every purge with 23503 on post_restrictions_post_id_fkey,
// every 30 s, forever. The released rows could not be deleted either:
// post_restriction_events references them and is append-only.

package postgres_test

import (
	"errors"
	"testing"
	"time"

	store "github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// purgeIT pins every soft-deleted post it makes to one ancient instant so
// the scan's cutoff selects only this test's posts.
type purgeIT struct {
	*restrictionIT
	deletedAt time.Time
}

func newPurgeIT(t *testing.T) *purgeIT {
	at := time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(time.Now().UnixNano()%1e9) * time.Microsecond)
	return &purgeIT{restrictionIT: newRestrictionIT(t), deletedAt: at}
}

func (p *purgeIT) deletedPost() uuid.UUID {
	p.t.Helper()
	id := p.seed("public", "approved", "long_video")
	if _, err := p.pool.Exec(p.ctx, `UPDATE posts SET deleted_at = $2 WHERE id = $1`, id, p.deletedAt); err != nil {
		p.t.Fatal(err)
	}
	return id
}

func (p *purgeIT) listed(id uuid.UUID) bool {
	p.t.Helper()
	cands, err := p.s.ListPurgeablePosts(p.ctx, p.deletedAt, 1000)
	if err != nil {
		p.t.Fatal(err)
	}
	for _, c := range cands {
		if c.PostID == id {
			return true
		}
	}
	return false
}

func (p *purgeIT) exists(id uuid.UUID) bool {
	p.t.Helper()
	var n int
	if err := p.pool.QueryRow(p.ctx, `SELECT COUNT(*) FROM posts WHERE id = $1`, id).Scan(&n); err != nil {
		p.t.Fatal(err)
	}
	return n == 1
}

func (p *purgeIT) count(sql string, args ...any) int {
	p.t.Helper()
	var n int
	if err := p.pool.QueryRow(p.ctx, sql, args...).Scan(&n); err != nil {
		p.t.Fatal(err)
	}
	return n
}

// The dev case: a hold placed and released. The post purges, and the
// restriction row and both events stay as the hold's audit.
func TestPurgeHoldIT_ReleasedHoldPurgesAndKeepsTheAudit(t *testing.T) {
	p := newPurgeIT(t)
	post := p.deletedPost()
	caseID := uuid.New()
	p.apply(p.cmd(post, caseID, store.RestrictionActionPlaceHold, store.RestrictionStateAbsent, 1))
	p.apply(p.cmd(post, caseID, store.RestrictionActionReleaseHold, store.RestrictionStateActive, 2))

	if !p.listed(post) {
		t.Fatal("a post whose holds are all released is not offered for purge")
	}
	if _, err := p.s.PurgePost(p.ctx, post); err != nil {
		t.Fatalf("purge of a post with a released hold: %v", err)
	}
	if p.exists(post) {
		t.Fatal("post survived the purge")
	}
	if n := p.count(`SELECT COUNT(*) FROM post_restrictions WHERE post_id = $1 AND state = 'released'`, post); n != 1 {
		t.Fatalf("released restriction rows after purge = %d, want 1 (the audit outlives the post)", n)
	}
	if n := p.count(`SELECT COUNT(*) FROM post_restriction_events WHERE post_id = $1`, post); n != 2 {
		t.Fatalf("restriction events after purge = %d, want 2", n)
	}
	// And a new hold on the purged post is refused, not written to a ghost.
	if _, err := p.s.ApplyPostRestriction(p.ctx, p.cmd(post, uuid.New(), store.RestrictionActionPlaceHold, store.RestrictionStateAbsent, 1)); !errors.Is(err, store.ErrRestrictionSubjectNotFound) {
		t.Fatalf("hold on a purged post: err = %v, want ErrRestrictionSubjectNotFound", err)
	}
}

// An active hold: not offered, counted as held, and PurgePost itself
// refuses it (the guard under the row lock, independent of the scan).
func TestPurgeHoldIT_ActiveHoldIsNeverPurged(t *testing.T) {
	p := newPurgeIT(t)
	heldBefore, err := p.s.CountHeldPurgeablePosts(p.ctx, p.deletedAt)
	if err != nil {
		t.Fatal(err)
	}
	post := p.deletedPost()
	caseID := uuid.New()
	p.apply(p.cmd(post, caseID, store.RestrictionActionPlaceHold, store.RestrictionStateAbsent, 1))

	if p.listed(post) {
		t.Fatal("a held post was offered for purge")
	}
	if held, err := p.s.CountHeldPurgeablePosts(p.ctx, p.deletedAt); err != nil || held != heldBefore+1 {
		t.Fatalf("held count = %d (err %v), want %d", held, err, heldBefore+1)
	}
	queued, err := p.s.PurgePost(p.ctx, post)
	if !errors.Is(err, store.ErrPurgeHeld) {
		t.Fatalf("PurgePost on a held post: queued=%v err=%v, want ErrPurgeHeld", queued, err)
	}
	if !p.exists(post) {
		t.Fatal("a held post was purged")
	}
	if n := p.count(`SELECT COUNT(*) FROM post_purge_media WHERE post_id = $1`, post); n != 0 {
		t.Fatalf("a refused purge queued %d media rows", n)
	}

	// Released: offered again and purged.
	p.apply(p.cmd(post, caseID, store.RestrictionActionReleaseHold, store.RestrictionStateActive, 2))
	if !p.listed(post) {
		t.Fatal("post not offered after its hold was released")
	}
	if _, err := p.s.PurgePost(p.ctx, post); err != nil || p.exists(post) {
		t.Fatalf("purge after release: err=%v exists=%v", err, p.exists(post))
	}
}

// A failed purge backs the post off; the scan skips it until the back-off
// is due; a successful purge clears the failure row.
func TestPurgeHoldIT_FailedPurgeBacksOff(t *testing.T) {
	p := newPurgeIT(t)
	post := p.deletedPost()

	attempts, next, err := p.s.DeferPurgePost(p.ctx, post, "boom")
	if err != nil || attempts != 1 {
		t.Fatalf("first defer: attempts=%d err=%v", attempts, err)
	}
	if d := time.Until(next); d < time.Minute || d > 3*time.Minute {
		t.Fatalf("first back-off is %s, want about 2m", d)
	}
	if p.listed(post) {
		t.Fatal("a backing-off post was offered for purge")
	}
	attempts, next, err = p.s.DeferPurgePost(p.ctx, post, "boom again")
	if err != nil || attempts != 2 {
		t.Fatalf("second defer: attempts=%d err=%v", attempts, err)
	}
	if d := time.Until(next); d < 3*time.Minute || d > 5*time.Minute {
		t.Fatalf("second back-off is %s, want about 4m", d)
	}
	if _, err := p.pool.Exec(p.ctx, `UPDATE post_purge_failures SET attempts = 30 WHERE post_id = $1`, post); err != nil {
		t.Fatal(err)
	}
	if _, next, err = p.s.DeferPurgePost(p.ctx, post, "still"); err != nil {
		t.Fatal(err)
	}
	if d := time.Until(next); d > 24*time.Hour+time.Minute || d < 23*time.Hour {
		t.Fatalf("back-off after many attempts is %s, want the one-day cap", d)
	}

	if _, err := p.pool.Exec(p.ctx, `UPDATE post_purge_failures SET next_attempt_at = NOW() - interval '1 second' WHERE post_id = $1`, post); err != nil {
		t.Fatal(err)
	}
	if !p.listed(post) {
		t.Fatal("post not offered once its back-off was due")
	}
	if _, err := p.s.PurgePost(p.ctx, post); err != nil {
		t.Fatal(err)
	}
	if n := p.count(`SELECT COUNT(*) FROM post_purge_failures WHERE post_id = $1`, post); n != 0 {
		t.Fatalf("failure row survived a successful purge (%d)", n)
	}
}

// Account purge hit the same FK for an author with a released hold.
func TestPurgeHoldIT_AccountPurgeWithReleasedHold(t *testing.T) {
	p := newPurgeIT(t)
	post := p.deletedPost()
	caseID := uuid.New()
	p.apply(p.cmd(post, caseID, store.RestrictionActionPlaceHold, store.RestrictionStateAbsent, 1))
	p.apply(p.cmd(post, caseID, store.RestrictionActionReleaseHold, store.RestrictionStateActive, 2))
	if err := p.s.PurgeUser(p.ctx, p.author); err != nil {
		t.Fatalf("account purge of an author with a released hold: %v", err)
	}
	if p.exists(post) {
		t.Fatal("post survived the account purge")
	}
}

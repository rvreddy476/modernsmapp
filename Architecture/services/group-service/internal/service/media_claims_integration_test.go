//go:build integration

package service

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/group-service/database"
	"github.com/atpost/group-service/internal/claimsync"
	"github.com/atpost/group-service/internal/mediaclaims"
	"github.com/atpost/group-service/internal/mediaclient"
	"github.com/atpost/group-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

/*
DB-backed proof of the media claim protocol for groups (media_claims.go): a
group's avatar and cover, and an event's cover, hold a claim in media-service
from before the row names them until after it stops. The media-service side is
the claim-keeping fake at the end of this file; the group rows are real.
Run (CI only, *_test databases):

	GROUP_POSTGRES_DSN=…/group_it_test go test -tags integration ./internal/service/ -run MediaClaimsIT -p 1
*/

func setupMediaClaimsIT(t *testing.T) (*Service, *store.Store, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("GROUP_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("GROUP_POSTGRES_DSN is required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatalf("refusing to run against database %q", cfg.ConnConfig.Database)
	}
	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, database.SetupSQL); err != nil {
		t.Fatal(err)
	}
	// The columns groupColumns reads, without the full migration chain (002
	// alters post-service's tables).
	if _, err := pool.Exec(ctx, `
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS handle TEXT;
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS category TEXT NOT NULL DEFAULT '';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS privacy_level TEXT NOT NULL DEFAULT 'public';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS join_mode TEXT NOT NULL DEFAULT 'open';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS who_can_post TEXT NOT NULL DEFAULT 'all_members';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS who_can_invite TEXT NOT NULL DEFAULT 'all_members';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS location TEXT NOT NULL DEFAULT '';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS language TEXT NOT NULL DEFAULT '';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS pending_request_count INT NOT NULL DEFAULT 0;
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS group_type TEXT NOT NULL DEFAULT 'public';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS max_members INT NOT NULL DEFAULT 0;
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS join_questions JSONB DEFAULT '[]';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS topic_tags TEXT[] NOT NULL DEFAULT '{}';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS comment_permission TEXT NOT NULL DEFAULT 'all_members';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS member_list_visible BOOLEAN NOT NULL DEFAULT true;
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS link_sharing BOOLEAN NOT NULL DEFAULT true;
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS is_mature BOOLEAN NOT NULL DEFAULT false;
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS allow_anonymous_posts BOOLEAN NOT NULL DEFAULT false;
		ALTER TABLE group_members ADD COLUMN IF NOT EXISTS id UUID DEFAULT gen_random_uuid();
		ALTER TABLE group_members ADD COLUMN IF NOT EXISTS invited_by_user_id UUID;
		ALTER TABLE group_members ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
		ALTER TABLE group_members ADD COLUMN IF NOT EXISTS removed_at TIMESTAMPTZ;
		ALTER TABLE group_members ADD COLUMN IF NOT EXISTS removed_by_user_id UUID;
		ALTER TABLE group_members ADD COLUMN IF NOT EXISTS removal_reason TEXT;
	`); err != nil {
		t.Fatal(err)
	}
	// Nothing listens here: the cache invalidation fails fast and is ignored.
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: 200 * time.Millisecond})
	t.Cleanup(func() { _ = rdb.Close() })
	st := store.New(pool)
	return New(st, rdb, "", "", "", ""), st, pool
}

// seedAdminGroup makes an active group whose creator is its admin.
func seedAdminGroup(t *testing.T, pool *pgxpool.Pool, creator uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO groups (name, creator_id, handle) VALUES ('claims', $1, $2) RETURNING id`,
		creator, "mc-"+uuid.NewString()[:12]).Scan(&id); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO group_members (group_id, user_id, role, status) VALUES ($1, $2, 'admin', 'active')`, id, creator); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	return id
}

func TestMediaClaimsIT_GroupLifecycle(t *testing.T) {
	svc, st, pool := setupMediaClaimsIT(t)
	ctx := context.Background()
	admin := uuid.New()
	groupID := seedAdminGroup(t, pool, admin)
	avatar, cover, next, eventCover := readyAsset(admin), readyAsset(admin), readyAsset(admin), readyAsset(admin)
	f := newFakeMedia(avatar, cover, next, eventCover)
	svc.WithMedia(mediaclaims.New(f, f))

	if err := svc.UpdateGroup(ctx, admin, groupID, "claims", "", &avatar.ID, &cover.ID, "public"); err != nil {
		t.Fatalf("set media: %v", err)
	}
	if !f.holds(mediaclient.KindGroupAvatar, groupID, avatar.ID) || !f.holds(mediaclient.KindGroupCover, groupID, cover.ID) {
		t.Fatalf("a group does not hold claims on its media: %v", f.held)
	}
	// nil keeps the current cover; only the avatar changes.
	if err := svc.UpdateGroup(ctx, admin, groupID, "claims", "", &next.ID, nil, "public"); err != nil {
		t.Fatalf("replace avatar: %v", err)
	}
	if !f.holds(mediaclient.KindGroupAvatar, groupID, next.ID) || f.holds(mediaclient.KindGroupAvatar, groupID, avatar.ID) {
		t.Fatalf("after replacing the avatar: %v", f.held)
	}
	g, err := st.GetGroupByID(ctx, groupID)
	if err != nil || g.CoverMediaID == nil || *g.CoverMediaID != cover.ID || !f.holds(mediaclient.KindGroupCover, groupID, cover.ID) {
		t.Fatalf("a save that did not set the cover changed or released it: %v %+v", err, g)
	}

	event := &store.GroupEvent{Title: "meetup", StartAt: time.Now().Add(time.Hour), CoverMediaID: &eventCover.ID, RSVPEnabled: true}
	if err := svc.CreateGroupEvent(ctx, admin, groupID, event); err != nil {
		t.Fatalf("create event: %v", err)
	}
	if !f.holds(mediaclient.KindGroupEventCover, event.ID, eventCover.ID) {
		t.Fatal("an event does not hold a claim on its cover, or the claim names another id than the row")
	}
	if got, err := st.GetGroupEvent(ctx, event.ID); err != nil || got.ID != event.ID {
		t.Fatalf("the event row is not under the claimed id: %v", err)
	}

	if err := svc.DeleteGroup(ctx, admin, groupID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(f.held) != 0 {
		t.Fatalf("a deleted group still holds media: %v", f.held)
	}
}

func TestMediaClaimsIT_RefusedAttachWritesNothing(t *testing.T) {
	svc, st, pool := setupMediaClaimsIT(t)
	ctx := context.Background()
	admin := uuid.New()
	groupID := seedAdminGroup(t, pool, admin)
	theirs, pending := readyAsset(uuid.New()), readyAsset(admin)
	pending.ProcessingStatus = "pending_upload"
	f := newFakeMedia(theirs, pending)
	svc.WithMedia(mediaclaims.New(f, f))

	if err := svc.UpdateGroup(ctx, admin, groupID, "claims", "", &theirs.ID, nil, "public"); !errors.Is(err, mediaclaims.ErrMediaNotOwned) {
		t.Fatalf("someone else's avatar = %v", err)
	}
	if err := svc.UpdateGroup(ctx, admin, groupID, "claims", "", nil, &pending.ID, "public"); !errors.Is(err, mediaclaims.ErrMediaNotReady) {
		t.Fatalf("an unconfirmed cover = %v", err)
	}
	g, err := st.GetGroupByID(ctx, groupID)
	if err != nil || g.AvatarMediaID != nil || g.CoverMediaID != nil {
		t.Fatalf("refused media was written: %v %+v", err, g)
	}
	event := &store.GroupEvent{Title: "x", StartAt: time.Now(), CoverMediaID: &theirs.ID}
	if err := svc.CreateGroupEvent(ctx, admin, groupID, event); !errors.Is(err, mediaclaims.ErrMediaNotOwned) {
		t.Fatalf("someone else's event cover = %v", err)
	}
	if events, _ := st.ListGroupEvents(ctx, groupID, 10, 0); len(events) != 0 {
		t.Fatal("an event with a refused cover was written")
	}
	if len(f.held) != 0 {
		t.Fatalf("a refused attach left claims: %v", f.held)
	}

	// Unconfigured: adding media is refused, a save without media is not.
	svc.WithMedia(nil)
	mine := readyAsset(admin)
	if err := svc.UpdateGroup(ctx, admin, groupID, "claims", "", &mine.ID, nil, "public"); !errors.Is(err, mediaclaims.ErrMediaUnavailable) {
		t.Fatalf("no media-service = %v", err)
	}
	if err := svc.UpdateGroup(ctx, admin, groupID, "renamed", "", nil, nil, "public"); err != nil {
		t.Fatalf("a save without new media needed media-service: %v", err)
	}
}

func TestMediaClaimsIT_ReconcileBackfillsAndReleases(t *testing.T) {
	_, st, pool := setupMediaClaimsIT(t)
	ctx := context.Background()
	admin := uuid.New()
	groupID := seedAdminGroup(t, pool, admin)
	cover, eventCover := readyAsset(admin), readyAsset(admin)
	f := newFakeMedia(cover, eventCover)
	// Rows written before the protocol: they name media with no claim.
	if _, err := pool.Exec(ctx, `UPDATE groups SET cover_media_id = $2 WHERE id = $1`, groupID, cover.ID); err != nil {
		t.Fatal(err)
	}
	var eventID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO group_events (group_id, creator_id, title, start_at, cover_media_id)
		VALUES ($1, $2, 'legacy', NOW(), $3) RETURNING id`, groupID, admin.String(), eventCover.ID).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	pagers := []claimsync.Pager{st.GroupMediaClaimRows, st.GroupEventMediaClaimRows}
	if _, err := claimsync.Run(ctx, st, f, pagers, false); err != nil || len(f.held) != 0 {
		t.Fatalf("a dry run sent claims: %v %v", err, f.held)
	}
	if rep, err := claimsync.Run(ctx, st, f, pagers, true); err != nil || rep.Failed != 0 {
		t.Fatalf("apply: %v %s", err, rep)
	}
	if !f.holds(mediaclient.KindGroupCover, groupID, cover.ID) || !f.holds(mediaclient.KindGroupEventCover, eventID, eventCover.ID) {
		t.Fatalf("the backfill did not claim legacy media: %v", f.held)
	}
	if _, err := pool.Exec(ctx, `UPDATE group_events SET status = 'cancelled' WHERE id = $1`, eventID); err != nil {
		t.Fatal(err)
	}
	if _, err := claimsync.Run(ctx, st, f, pagers, true); err != nil {
		t.Fatal(err)
	}
	if f.holds(mediaclient.KindGroupEventCover, eventID, eventCover.ID) {
		t.Fatal("the repair did not release a cancelled event's cover")
	}
	if !f.holds(mediaclient.KindGroupCover, groupID, cover.ID) {
		t.Fatal("the repair released a live group's cover")
	}
	if _, err := pool.Exec(ctx, `UPDATE groups SET status = 'deleted' WHERE id = $1`, groupID); err != nil {
		t.Fatal(err)
	}
	if _, err := claimsync.Run(ctx, st, f, pagers, true); err != nil {
		t.Fatal(err)
	}
	if len(f.held) != 0 {
		t.Fatalf("a deleted group still holds media after the repair: %v", f.held)
	}
}

// The second writer of one row waits for the first; another row does not.
func TestMediaClaimsIT_HolderLockSerialises(t *testing.T) {
	_, st, _ := setupMediaClaimsIT(t)
	ctx := context.Background()
	ref := uuid.New()
	unlock, err := st.LockMediaHolder(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.LockMediaHolder(ctx, uuid.New())
	if err != nil {
		t.Fatalf("a different row's lock waited: %v", err)
	}
	other()
	got := make(chan struct{})
	go func() {
		u, err := st.LockMediaHolder(ctx, ref)
		if err == nil {
			u()
		}
		close(got)
	}()
	select {
	case <-got:
		t.Fatal("a second writer took the lock while the first held it")
	case <-time.After(300 * time.Millisecond):
	}
	unlock()
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("the lock was not freed by unlock")
	}
}

// fakeMedia is media-service for these integration tests: an asset table and the
// claims it keeps (Lookup, Claims and claimsync.Claims).
type fakeMedia struct {
	mu     sync.Mutex
	assets map[uuid.UUID]*mediaclient.Asset
	held   map[string]map[uuid.UUID]bool
}

func newFakeMedia(assets ...*mediaclient.Asset) *fakeMedia {
	f := &fakeMedia{assets: map[uuid.UUID]*mediaclient.Asset{}, held: map[string]map[uuid.UUID]bool{}}
	for _, a := range assets {
		f.assets[a.ID] = a
	}
	return f
}

func fakeKey(kind string, ref uuid.UUID) string { return kind + "|" + ref.String() }

func (f *fakeMedia) Get(_ context.Context, id uuid.UUID) (*mediaclient.Asset, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a, ok := f.assets[id]; ok {
		return a, nil
	}
	return nil, mediaclient.ErrNotFound
}

func (f *fakeMedia) Claim(_ context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range ids {
		if _, ok := f.assets[id]; !ok {
			return mediaclient.ErrNotFound
		}
	}
	k := fakeKey(kind, ref)
	if f.held[k] == nil {
		f.held[k] = map[uuid.UUID]bool{}
	}
	for _, id := range ids {
		f.held[k][id] = true
	}
	return nil
}

func (f *fakeMedia) Release(_ context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(ids) == 0 {
		delete(f.held, fakeKey(kind, ref))
	}
	for _, id := range ids {
		delete(f.held[fakeKey(kind, ref)], id)
	}
	return nil
}

func (f *fakeMedia) ReleaseExcept(_ context.Context, kind string, ref uuid.UUID, keep []uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id := range f.held[fakeKey(kind, ref)] {
		if !slices.Contains(keep, id) {
			delete(f.held[fakeKey(kind, ref)], id)
		}
	}
	return nil
}

func (f *fakeMedia) holds(kind string, ref, id uuid.UUID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.held[fakeKey(kind, ref)][id]
}

func readyAsset(owner uuid.UUID) *mediaclient.Asset {
	return &mediaclient.Asset{ID: uuid.New(), UploaderID: owner, ProcessingStatus: "ready", ModerationStatus: "passed"}
}

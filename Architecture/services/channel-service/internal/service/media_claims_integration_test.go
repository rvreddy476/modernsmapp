// DB-backed proof of the media claim protocol (media_claims.go): every channel
// row that names media holds a claim on it in media-service, from before the
// row is written until after it stops naming it. media-service is the claim-
// keeping fake from update_media_test.go; the channel rows are real.
// TEST_PG_DSN on a "_test" database, CI only.
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/channel-service/internal/claimsync"
	"github.com/atpost/channel-service/internal/mediaclient"
	"github.com/atpost/channel-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func seedOwnerMember(t *testing.T, pool *pgxpool.Pool, channelID, owner uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO channel_members (channel_id, user_id, role) VALUES ($1, $2, 'owner')
		ON CONFLICT (channel_id, user_id) DO UPDATE SET role = 'owner'`, channelID, owner); err != nil {
		t.Fatalf("seed owner member: %v", err)
	}
}

func claimFake(assets ...*mediaclient.Asset) *fakeMedia {
	f := &fakeMedia{assets: map[uuid.UUID]*mediaclient.Asset{}}
	for _, a := range assets {
		f.assets[a.ID] = a
	}
	return f
}

func TestMediaClaimsIT_UpdateLifecycle(t *testing.T) {
	svc, _, pool := setupReactionCountIT(t)
	ctx := context.Background()
	owner := uuid.New()
	channelID := seedOwnedChannel(t, pool, owner)
	first, second := asset(owner, "ready", "passed"), asset(owner, "ready", "passed")
	f := claimFake(first, second)
	svc.WithMedia(f)

	u, err := svc.CreateUpdate(ctx, channelID, owner, CreateUpdateParams{Body: "hi", MediaIDs: []uuid.UUID{first.ID}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !f.holds(claimKindUpdate, u.ID, first.ID) {
		t.Fatal("a created update does not hold a claim on its media")
	}

	if _, err := svc.EditUpdate(ctx, channelID, u.ID, owner, CreateUpdateParams{MediaIDs: []uuid.UUID{second.ID}}); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if !f.holds(claimKindUpdate, u.ID, second.ID) || f.holds(claimKindUpdate, u.ID, first.ID) {
		t.Fatalf("after replacing the media: held %v", f.held)
	}

	seedOwnerMember(t, pool, channelID, owner) // DeleteUpdate checks the member role
	if err := svc.DeleteUpdate(ctx, channelID, u.ID, owner); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if f.holds(claimKindUpdate, u.ID, second.ID) {
		t.Fatal("a deleted update still holds its media")
	}
}

// The owner's delete won the race: the claim finds no asset and the update is
// never written, so nothing can dangle.
func TestMediaClaimsIT_RefusedClaimWritesNothing(t *testing.T) {
	svc, _, pool := setupReactionCountIT(t)
	ctx := context.Background()
	owner := uuid.New()
	channelID := seedOwnedChannel(t, pool, owner)
	gone := asset(owner, "ready", "passed")
	f := claimFake(gone)
	svc.WithMedia(f)
	// Get still answers (the gate ran first); the asset is deleted before
	// the claim, which is what media-service's row lock decides.
	svc.WithMediaClaims(&claimsWithout{fakeMedia: f, missing: gone.ID})

	if _, err := svc.CreateUpdate(ctx, channelID, owner, CreateUpdateParams{Body: "hi", MediaIDs: []uuid.UUID{gone.ID}}); !errors.Is(err, ErrMediaNotFound) {
		t.Fatalf("create after the owner's delete won = %v, want ErrMediaNotFound", err)
	}
	if n := countUpdates(t, pool, channelID); n != 0 {
		t.Fatalf("a refused claim still wrote %d update(s)", n)
	}
}

func TestMediaClaimsIT_ChannelPictureAndBanner(t *testing.T) {
	svc, _, pool := setupReactionCountIT(t)
	ctx := context.Background()
	owner := uuid.New()
	channelID := seedOwnedChannel(t, pool, owner)
	seedOwnerMember(t, pool, channelID, owner)
	avatar1, avatar2, banner := asset(owner, "ready", "passed"), asset(owner, "ready", "passed"), asset(owner, "ready", "passed")
	f := claimFake(avatar1, avatar2, banner)
	svc.WithMedia(f)

	if _, err := svc.UpdateChannel(ctx, channelID, owner, UpdateChannelParams{AvatarMediaID: &avatar1.ID, BannerMediaID: &banner.ID}); err != nil {
		t.Fatalf("set picture and banner: %v", err)
	}
	if !f.holds(claimKindAvatar, channelID, avatar1.ID) || !f.holds(claimKindBanner, channelID, banner.ID) {
		t.Fatalf("held %v", f.held)
	}
	if _, err := svc.UpdateChannel(ctx, channelID, owner, UpdateChannelParams{AvatarMediaID: &avatar2.ID}); err != nil {
		t.Fatalf("replace picture: %v", err)
	}
	if !f.holds(claimKindAvatar, channelID, avatar2.ID) || f.holds(claimKindAvatar, channelID, avatar1.ID) ||
		!f.holds(claimKindBanner, channelID, banner.ID) {
		t.Fatalf("after replacing the picture: held %v", f.held)
	}

	missing := uuid.New()
	if _, err := svc.UpdateChannel(ctx, channelID, owner, UpdateChannelParams{AvatarMediaID: &missing}); !errors.Is(err, ErrMediaNotFound) {
		t.Fatalf("a picture that does not exist = %v, want ErrMediaNotFound", err)
	}
	var stored uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT avatar_media_id FROM broadcast_channels WHERE id = $1`, channelID).Scan(&stored); err != nil || stored != avatar2.ID {
		t.Fatalf("a refused picture change was written: %v %v", stored, err)
	}

	u, err := svc.CreateUpdate(ctx, channelID, owner, CreateUpdateParams{Body: "x", MediaIDs: []uuid.UUID{avatar1.ID}})
	if err != nil {
		t.Fatalf("create update: %v", err)
	}
	if err := svc.DeleteChannel(ctx, channelID, owner); err != nil {
		t.Fatalf("delete channel: %v", err)
	}
	if f.holds(claimKindAvatar, channelID, avatar2.ID) || f.holds(claimKindBanner, channelID, banner.ID) ||
		f.holds(claimKindUpdate, u.ID, avatar1.ID) {
		t.Fatalf("a deleted channel still holds media: %v", f.held)
	}
}

// claimsWithout is fakeMedia whose claim refuses one id as gone.
type claimsWithout struct {
	*fakeMedia
	missing uuid.UUID
}

func (c *claimsWithout) Claim(ctx context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error {
	for _, id := range ids {
		if id == c.missing {
			return mediaclient.ErrNotFound
		}
	}
	return c.fakeMedia.Claim(ctx, kind, ref, ids)
}

// The backfill/reconcile pass over the real tables: a live update and a live
// channel picture are claimed, a deleted update is released.
func TestMediaClaimsIT_ReconcileReadsTheRealTables(t *testing.T) {
	_, st, pool := setupReactionCountIT(t)
	ctx := context.Background()
	owner := uuid.New()
	channelID := seedOwnedChannel(t, pool, owner)
	avatar, live, dead := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `UPDATE broadcast_channels SET avatar_media_id = $2 WHERE id = $1`, channelID, avatar); err != nil {
		t.Fatal(err)
	}
	var liveUpdate, deadUpdate uuid.UUID
	for _, tc := range []struct {
		id     *uuid.UUID
		media  uuid.UUID
		status string
	}{{&liveUpdate, live, "published"}, {&deadUpdate, dead, "deleted"}} {
		if err := pool.QueryRow(ctx, `
			INSERT INTO channel_updates (channel_id, author_id, body, media_ids, status)
			VALUES ($1, $2, 'reconcile', $3, $4) RETURNING id`,
			channelID, owner, []uuid.UUID{tc.media}, tc.status).Scan(tc.id); err != nil {
			t.Fatalf("seed update: %v", err)
		}
	}

	rec := &journalClaims{}
	rep, err := claimsync.Run(ctx, st, rec, true)
	if err != nil {
		t.Fatalf("reconcile: %v (%s)", err, rep)
	}
	for _, want := range []string{
		"claim channel_avatar " + channelID.String() + " [" + avatar.String() + "]",
		"claim channel_update " + liveUpdate.String() + " [" + live.String() + "]",
		"release channel_update " + deadUpdate.String() + " []",
	} {
		if !rec.saw(want) {
			t.Errorf("reconcile did not send %q", want)
		}
	}
	if rec.saw("claim channel_update " + deadUpdate.String()) {
		t.Error("reconcile claimed media for a deleted update")
	}
}

// journalClaims accepts everything and remembers it.
type journalClaims struct{ lines []string }

func (j *journalClaims) Claim(_ context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error {
	j.lines = append(j.lines, fmt.Sprintf("claim %s %s %v", kind, ref, ids))
	return nil
}

func (j *journalClaims) Release(_ context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error {
	j.lines = append(j.lines, fmt.Sprintf("release %s %s %v", kind, ref, ids))
	return nil
}

func (j *journalClaims) ReleaseExcept(_ context.Context, kind string, ref uuid.UUID, keep []uuid.UUID) error {
	j.lines = append(j.lines, fmt.Sprintf("release-except %s %s %v", kind, ref, keep))
	return nil
}

func (j *journalClaims) saw(prefix string) bool {
	for _, l := range j.lines {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}

// Concurrent edits of one update, each reading the row first: without the
// per-row media lock one edit's release can remove a claim another's write
// relies on. Whatever order they land in, every id the final row names must
// still be claimed.
func TestMediaClaimsIT_ConcurrentEditsNeverLeaveAnUnclaimedReference(t *testing.T) {
	svc, st, pool := setupReactionCountIT(t)
	ctx := context.Background()
	owner := uuid.New()
	channelID := seedOwnedChannel(t, pool, owner)
	x, z := asset(owner, "ready", "passed"), asset(owner, "ready", "passed")
	f := claimFake(x, z)
	svc.WithMedia(f)
	u, err := svc.CreateUpdate(ctx, channelID, owner, CreateUpdateParams{Body: "race", MediaIDs: []uuid.UUID{x.ID}})
	if err != nil {
		t.Fatal(err)
	}
	sets := [][]uuid.UUID{{}, {x.ID, z.ID}, {z.ID}, {x.ID}}
	for round := 0; round < 10; round++ {
		var wg sync.WaitGroup
		for i := range sets {
			wg.Add(1)
			go func(ids []uuid.UUID) {
				defer wg.Done()
				if _, err := svc.EditUpdate(ctx, channelID, u.ID, owner, CreateUpdateParams{MediaIDs: ids}); err != nil {
					t.Errorf("edit: %v", err)
				}
			}(sets[i])
		}
		wg.Wait()
		got, err := st.GetUpdate(ctx, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range got.MediaIDs {
			if !f.holds(claimKindUpdate, u.ID, id) {
				t.Fatalf("round %d: the update names %s with no claim on it", round, id)
			}
		}
	}
}

// The lock is exclusive per row and free for other rows. The same-row attempt
// is made twice: through the same Store (queued by the in-process holderGate)
// and through a second Store on the same database, standing in for another
// replica, which only Postgres's advisory lock can hold back.
func TestMediaClaimsIT_HolderLockSerialises(t *testing.T) {
	_, st, pool := setupReactionCountIT(t)
	ctx := context.Background()
	ref := uuid.New()
	unlock, err := st.LockMediaHolder(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]*store.Store{"same store": st, "another replica": store.New(pool)} {
		short, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
		u2, err := s.LockMediaHolder(short, ref)
		cancel()
		if err == nil {
			u2()
			t.Fatalf("%s: a second holder of the same row's lock got it while the first held it", name)
		}
	}
	other, err := st.LockMediaHolder(ctx, uuid.New())
	if err != nil {
		t.Fatalf("another row's lock must be free: %v", err)
	}
	other()
	unlock()
	again, err := st.LockMediaHolder(ctx, ref)
	if err != nil {
		t.Fatalf("after unlock the row's lock must be free: %v", err)
	}
	again()
}

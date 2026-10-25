// DB-backed proof that CreateUpdate and EditUpdate refuse media ids that are
// missing, another user's, or not finished uploading, and write nothing when
// they do (update_media.go). media-service is a fake; the channel rows are
// real. Uses the reaction-count rig: TEST_PG_DSN on a "_test" database, CI.
package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/atpost/channel-service/internal/mediaclient"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func seedOwnedChannel(t *testing.T, pool *pgxpool.Pool, owner uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	handle := "um" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO broadcast_channels (owner_id, handle, name, channel_type, status)
		VALUES ($1, $2, 'update media channel', 'public', 'active') RETURNING id`,
		owner, handle).Scan(&id); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	return id
}

func countUpdates(t *testing.T, pool *pgxpool.Pool, channelID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM channel_updates WHERE channel_id = $1`, channelID).Scan(&n); err != nil {
		t.Fatalf("count updates: %v", err)
	}
	return n
}

func TestUpdateMediaIT_CreateAndEditRefuseUnattachableMedia(t *testing.T) {
	svc, st, pool := setupReactionCountIT(t)
	ctx := context.Background()
	owner := uuid.New()
	channelID := seedOwnedChannel(t, pool, owner)

	own := asset(owner, "ready", "passed")
	foreign := asset(uuid.New(), "ready", "passed")
	pending := asset(owner, "pending_upload", "pending")
	f := &fakeMedia{assets: map[uuid.UUID]*mediaclient.Asset{}}
	for _, a := range []*mediaclient.Asset{own, foreign, pending} {
		f.assets[a.ID] = a
	}
	svc.WithMedia(f)

	for name, tc := range map[string]struct {
		id   uuid.UUID
		want error
	}{
		"missing":        {uuid.New(), ErrMediaNotFound},
		"another user's": {foreign.ID, ErrMediaNotOwned},
		"pending_upload": {pending.ID, ErrMediaNotReady},
	} {
		_, err := svc.CreateUpdate(ctx, channelID, owner, CreateUpdateParams{Body: "hi", MediaIDs: []uuid.UUID{tc.id}})
		if !errors.Is(err, tc.want) {
			t.Fatalf("create with %s media = %v, want %v", name, err, tc.want)
		}
	}
	if n := countUpdates(t, pool, channelID); n != 0 {
		t.Fatalf("refused creates wrote %d rows", n)
	}

	u, err := svc.CreateUpdate(ctx, channelID, owner, CreateUpdateParams{Body: "hi", MediaIDs: []uuid.UUID{own.ID}})
	if err != nil {
		t.Fatalf("create with own ready media: %v", err)
	}

	for name, id := range map[string]uuid.UUID{"another user's": foreign.ID, "pending_upload": pending.ID} {
		if _, err := svc.EditUpdate(ctx, channelID, u.ID, owner, CreateUpdateParams{MediaIDs: []uuid.UUID{own.ID, id}}); err == nil {
			t.Fatalf("edit adding %s media was accepted", name)
		}
	}
	got, err := st.GetUpdate(ctx, u.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(got.MediaIDs) != 1 || got.MediaIDs[0] != own.ID {
		t.Fatalf("refused edits changed media_ids to %v", got.MediaIDs)
	}
}

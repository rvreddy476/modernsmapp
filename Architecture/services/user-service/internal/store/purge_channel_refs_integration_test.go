//go:build integration

package store

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// lv-7 (long-video readiness audit): another user's video series or
// playlist naming the purged user's channel must not block the purge.
//
// post-service owns video_series and playlists in the shared app database;
// the user-service suite's database has neither, so the two tables are
// created here in the shape migration 012 left them: channel_id REFERENCES
// channels(id) with NO ON DELETE — the database post-service migration 086
// has not reached yet, the worst case. TEST_PG_DSN on a "_test" database,
// CI only (reuses channelOwnerIT's guard).
func TestPurgeIT_StrangersSeriesAndPlaylistDoNotBlockTheErasure(t *testing.T) {
	st, pool := channelOwnerIT(t)
	ctx := context.Background()
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS video_series (
			id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			creator_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			channel_id UUID REFERENCES channels(id),
			title      TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS playlists (
			id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			creator_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			channel_id UUID REFERENCES channels(id),
			title      TEXT NOT NULL DEFAULT ''
		)`,
	} {
		if _, err := pool.Exec(ctx, ddl); err != nil {
			t.Fatalf("stand-in table: %v", err)
		}
	}

	victim := seedClaimsUser(t, pool)
	stranger := seedClaimsUser(t, pool)
	ch := &Channel{UserID: victim, Handle: "pv" + strings.ReplaceAll(uuid.NewString(), "-", "")[:14], Name: "victim", CollabStatus: "closed"}
	if err := st.CreateChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	seriesID, playlistID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO video_series (id, creator_id, channel_id, title) VALUES ($1, $2, $3, 'x')`, seriesID, stranger, ch.ID); err != nil {
		t.Fatalf("seed stranger series: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO playlists (id, creator_id, channel_id, title) VALUES ($1, $2, $3, 'x')`, playlistID, stranger, ch.ID); err != nil {
		t.Fatalf("seed stranger playlist: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM video_series WHERE id = $1`, seriesID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM playlists WHERE id = $1`, playlistID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, stranger)
	})

	if err := st.PurgeUser(ctx, victim); err != nil {
		t.Fatalf("a stranger's series and playlist blocked the purge: %v", err)
	}

	var users, channels int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id = $1`, victim).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM channels WHERE id = $1`, ch.ID).Scan(&channels); err != nil {
		t.Fatal(err)
	}
	if users != 0 || channels != 0 {
		t.Fatalf("after the purge: %d users rows, %d channels rows; want none", users, channels)
	}
	// The stranger's rows survive, without the reference.
	for table, id := range map[string]uuid.UUID{"video_series": seriesID, "playlists": playlistID} {
		var ref *uuid.UUID
		if err := pool.QueryRow(ctx, `SELECT channel_id FROM `+table+` WHERE id = $1`, id).Scan(&ref); err != nil {
			t.Fatalf("%s row gone or unreadable: %v", table, err)
		}
		if ref != nil {
			t.Fatalf("%s.channel_id = %v after the purge; want NULL", table, *ref)
		}
	}
	if err := st.PurgeUser(ctx, victim); err != nil {
		t.Fatalf("a repeated purge must stay idempotent: %v", err)
	}
}

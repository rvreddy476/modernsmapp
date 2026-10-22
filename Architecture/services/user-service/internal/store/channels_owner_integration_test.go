//go:build integration

package store

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/atpost/user-service/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Security sweep #28 (21 Oct 2026): a non-owner's channel update or delete
// changes nothing and answers CHANNEL_NOT_FOUND; the owner's succeeds. A
// refused delete also leaves the channel's links (deleted earlier in the same
// transaction) in place. Requires TEST_PG_DSN on a database whose name ends in
// "_test" (user_it_test, -p 1); skipped when unset.

func channelOwnerIT(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping channel owner integration tests")
	}
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil || !strings.HasSuffix(cfg.Database, "_test") {
		t.Fatal("TEST_PG_DSN must parse and name a database ending in _test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	return New(pool), pool
}

func seedOwnerChannel(t *testing.T, st *Store, pool *pgxpool.Pool) (*Channel, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	owner := uuid.New()
	handle := "it" + strings.ReplaceAll(owner.String(), "-", "")[:16]
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (id, username, display_name) VALUES ($1, $2, $3)`,
		owner, handle, "Channel Owner"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	ch := &Channel{UserID: owner, Handle: handle, Name: "Original", Description: "before", CollabStatus: "closed"}
	if err := st.CreateChannel(ctx, ch); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := st.UpsertChannelLinks(ctx, ch.ID, []ChannelLink{{Title: "site", URL: "https://example.com", SortOrder: 0}}); err != nil {
		t.Fatalf("seed link: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM channel_links WHERE channel_id = $1`, ch.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM channels WHERE id = $1`, ch.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, owner)
	})
	return ch, owner
}

func isChannelNotFound(err error) bool {
	return err != nil && err.Error() == "CHANNEL_NOT_FOUND"
}

func TestIntegrationNonOwnerCannotUpdateChannel(t *testing.T) {
	st, pool := channelOwnerIT(t)
	ctx := context.Background()
	ch, owner := seedOwnerChannel(t, st, pool)

	hijack := "Hijacked"
	err := st.UpdateChannel(ctx, &ChannelUpdate{ID: ch.ID, UserID: uuid.New(), Name: &hijack})
	if !isChannelNotFound(err) {
		t.Fatalf("non-owner update: %v, want CHANNEL_NOT_FOUND", err)
	}
	got, err := st.GetChannelByID(ctx, ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Original" {
		t.Fatalf("a non-owner's update landed: name %q", got.Name)
	}

	renamed := "Renamed"
	if err := st.UpdateChannel(ctx, &ChannelUpdate{ID: ch.ID, UserID: owner, Name: &renamed}); err != nil {
		t.Fatalf("owner update: %v", err)
	}
	if got, _ := st.GetChannelByID(ctx, ch.ID); got == nil || got.Name != "Renamed" {
		t.Fatalf("owner update did not land: %+v", got)
	}
}

func TestIntegrationNonOwnerCannotDeleteChannel(t *testing.T) {
	st, pool := channelOwnerIT(t)
	ctx := context.Background()
	ch, owner := seedOwnerChannel(t, st, pool)

	if err := st.DeleteChannel(ctx, ch.ID, uuid.New()); !isChannelNotFound(err) {
		t.Fatalf("non-owner delete: %v, want CHANNEL_NOT_FOUND", err)
	}
	if got, err := st.GetChannelByID(ctx, ch.ID); err != nil || got == nil {
		t.Fatalf("a non-owner's delete removed the channel: %v", err)
	}
	links, err := st.GetChannelLinks(ctx, ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 {
		t.Fatalf("a refused delete still removed the links (%d left): the transaction did not roll back", len(links))
	}

	if err := st.DeleteChannel(ctx, ch.ID, owner); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
	if got, _ := st.GetChannelByID(ctx, ch.ID); got != nil {
		t.Fatalf("owner delete left the channel: %+v", got)
	}
}

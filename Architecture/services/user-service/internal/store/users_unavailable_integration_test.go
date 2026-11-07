//go:build integration

package store

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// US-M1: GetUser and GetUserByUsername flag a hidden (deactivated /
// deletion-scheduled) or soft-deleted row as Unavailable, so the handler can
// answer it like a missing account. Requires TEST_PG_DSN (see
// channels_owner_integration_test.go).
func TestIntegrationUnavailableUserIsFlagged(t *testing.T) {
	st, pool := channelOwnerIT(t)
	ctx := context.Background()

	seed := func(t *testing.T) (uuid.UUID, string) {
		t.Helper()
		id := uuid.New()
		handle := "un" + strings.ReplaceAll(id.String(), "-", "")[:16]
		if _, err := pool.Exec(ctx,
			`INSERT INTO users (id, username, display_name, created_at, updated_at) VALUES ($1, $2, 'U', now(), now())`,
			id, handle); err != nil {
			t.Fatalf("seed user: %v", err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id) })
		return id, handle
	}
	check := func(t *testing.T, id uuid.UUID, handle string, want bool) {
		t.Helper()
		byID, err := st.GetUser(ctx, id)
		if err != nil || byID == nil {
			t.Fatalf("GetUser: (%v, %v)", byID, err)
		}
		byName, err := st.GetUserByUsername(ctx, handle)
		if err != nil || byName == nil {
			t.Fatalf("GetUserByUsername: (%v, %v)", byName, err)
		}
		if byID.Unavailable != want || byName.Unavailable != want {
			t.Fatalf("Unavailable by id=%v by handle=%v, want %v", byID.Unavailable, byName.Unavailable, want)
		}
	}

	t.Run("active", func(t *testing.T) {
		id, handle := seed(t)
		check(t, id, handle, false)
	})
	t.Run("hidden", func(t *testing.T) {
		id, handle := seed(t)
		if err := st.SetUserHidden(ctx, id, true, "deactivated"); err != nil {
			t.Fatal(err)
		}
		check(t, id, handle, true)
		if err := st.SetUserHidden(ctx, id, false, "reactivated"); err != nil {
			t.Fatal(err)
		}
		check(t, id, handle, false)
	})
	t.Run("soft_deleted", func(t *testing.T) {
		id, handle := seed(t)
		if err := st.SoftDeleteUser(ctx, id); err != nil {
			t.Fatal(err)
		}
		check(t, id, handle, true)
	})
}

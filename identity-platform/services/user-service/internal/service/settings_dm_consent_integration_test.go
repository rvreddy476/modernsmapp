//go:build integration

package service

// Live PostgreSQL test for the DM consent model (2026-10-20): the boot-time
// value migration in database/setup.sql and the minor profile at creation.
//
//	USER_SETTINGS_IT_POSTGRES_DSN=postgres://.../user_dm_consent_it_test \
//	  go test -tags integration ./internal/service/ -run Integration
//
// Applies the REAL auth-service setup.sql (which owns usr.user_settings) and
// then this service's embedded setup.sql TWICE, the way two boots would.
// Refuses any database whose name does not end in "_test".

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atpost/identity-shared/store/schemabootstrap"
	"github.com/atpost/identity-user-service/database"
	"github.com/atpost/identity-user-service/internal/config"
	"github.com/atpost/identity-user-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func dmConsentPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("USER_SETTINGS_IT_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("USER_SETTINGS_IT_POSTGRES_DSN not set")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: invalid")
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatalf("refusing to run: database %q does not end in _test", cfg.ConnConfig.Database)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	authSQL, err := os.ReadFile(filepath.Join("..", "..", "..", "auth-service", "database", "setup.sql"))
	if err != nil {
		t.Fatalf("read auth-service setup.sql: %v", err)
	}
	if err := schemabootstrap.Apply(context.Background(), pool, string(authSQL)); err != nil {
		t.Fatalf("auth-service schema: %v", err)
	}
	return pool
}

func seedAuthUser(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `INSERT INTO auth.users (user_id, email) VALUES ($1, $2)`,
		id, id.String()+"@example.test"); err != nil {
		t.Fatalf("seed auth user: %v", err)
	}
}

type settingsRow struct {
	msg, call string
	version   int
}

func readRow(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) settingsRow {
	t.Helper()
	var r settingsRow
	if err := pool.QueryRow(context.Background(),
		`SELECT who_can_message, who_can_call, privacy_version FROM usr.user_settings WHERE user_id = $1`, id,
	).Scan(&r.msg, &r.call, &r.version); err != nil {
		t.Fatalf("read settings row: %v", err)
	}
	return r
}

func TestIntegrationDMConsentMigrationIdempotent(t *testing.T) {
	pool := dmConsentPool(t)
	ctx := context.Background()

	// Rows as an older build stored them.
	seeds := []struct {
		name            string
		msg, call       string
		under18, strict bool
		wantMsg         string
		wantCall        string
		wantBump        bool
	}{
		{"adult connections_only", "connections_only", "connections_only", false, false, "people_i_follow", "accepted_chats_only", true},
		{"adult mutual", "connections_and_mutual_followers", "no_one", false, false, "people_i_follow", "no_one", true},
		{"adult fof", "friends_of_friends_requests", "accepted_chats_only", false, false, "followers_message_requests", "accepted_chats_only", true},
		{"adult everyone", "everyone_message_requests", "accepted_chats_only", false, false, "everyone_message_requests", "accepted_chats_only", false},
		{"adult no_one", "no_one", "no_one", false, false, "no_one", "no_one", false},
		{"minor everyone", "everyone_message_requests", "no_one", true, false, "people_i_follow", "no_one", true},
		{"minor followers", "followers_message_requests", "no_one", true, false, "people_i_follow", "no_one", true},
		{"minor fof", "friends_of_friends_requests", "no_one", true, false, "people_i_follow", "no_one", true},
		// DM consent leaves this row's message/call values alone, but the
		// mention/tag clamp earlier in the same setup.sql (546eec063, 24 Oct)
		// moves a minor's pre-clamp who_can_mention/who_can_tag 'everyone'
		// default to 'following' and bumps privacy_version once.
		{"minor no_one", "no_one", "no_one", true, false, "no_one", "no_one", true},
		{"strict adult everyone", "everyone_message_requests", "no_one", false, true, "people_i_follow", "no_one", true},
	}
	ids := make([]uuid.UUID, len(seeds))
	for i, s := range seeds {
		ids[i] = uuid.New()
		seedAuthUser(t, pool, ids[i])
		if _, err := pool.Exec(ctx, `INSERT INTO usr.users (id) VALUES ($1)`, ids[i]); err != nil {
			t.Fatalf("seed usr user: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO usr.user_settings (user_id, who_can_message, who_can_call, under_18_mode, strict_privacy_mode)
			VALUES ($1, $2, $3, $4, $5)`, ids[i], s.msg, s.call, s.under18, s.strict); err != nil {
			t.Fatalf("seed settings %s: %v", s.name, err)
		}
	}

	var after [2][]settingsRow
	for run := 0; run < 2; run++ {
		if err := schemabootstrap.Apply(ctx, pool, database.SetupSQL); err != nil {
			t.Fatalf("user-service setup.sql run %d: %v", run+1, err)
		}
		for i := range seeds {
			after[run] = append(after[run], readRow(t, pool, ids[i]))
		}
	}
	for i, s := range seeds {
		got := after[0][i]
		if got.msg != s.wantMsg || got.call != s.wantCall {
			t.Errorf("%s: migrated to (%q, %q), want (%q, %q)", s.name, got.msg, got.call, s.wantMsg, s.wantCall)
		}
		if bumped := got.version > 1; bumped != s.wantBump {
			t.Errorf("%s: privacy_version=%d, want bumped=%v", s.name, got.version, s.wantBump)
		}
		if after[1][i] != got {
			t.Errorf("%s: second run changed the row: %+v -> %+v", s.name, got, after[1][i])
		}
	}

	// New rows inherit the moved column default.
	fresh := uuid.New()
	seedAuthUser(t, pool, fresh)
	if _, err := pool.Exec(ctx, `INSERT INTO usr.users (id) VALUES ($1)`, fresh); err != nil {
		t.Fatalf("seed usr user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO usr.user_settings (user_id) VALUES ($1)`, fresh); err != nil {
		t.Fatalf("seed default settings: %v", err)
	}
	if r := readRow(t, pool, fresh); r.msg != "everyone_message_requests" || r.call != "accepted_chats_only" {
		t.Fatalf("column defaults: got (%q, %q)", r.msg, r.call)
	}
}

func dmConsentService(t *testing.T, pool *pgxpool.Pool) *Service {
	t.Helper()
	// Nothing listens on port 1: cache invalidation fails fast and is logged,
	// which is exactly the best-effort behaviour UpdateSettings already has.
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 200 * time.Millisecond, MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	return New(store.New(pool), rdb, &config.Config{CacheTTL: time.Minute}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestIntegrationDMConsentDefaultsAtCreation(t *testing.T) {
	pool := dmConsentPool(t)
	ctx := context.Background()
	if err := schemabootstrap.Apply(ctx, pool, database.SetupSQL); err != nil {
		t.Fatalf("user-service setup.sql: %v", err)
	}
	svc := dmConsentService(t, pool)
	st := store.New(pool)

	assertMinor := func(t *testing.T, id uuid.UUID) *store.UserSettings {
		t.Helper()
		got, err := st.GetSettings(ctx, id)
		if err != nil || got == nil {
			t.Fatalf("get settings: %v", err)
		}
		if !got.Under18Mode || got.WhoCanMessage != "people_i_follow" || got.WhoCanCall != "no_one" ||
			got.WhoCanAddToGroups != "no_one" || got.WhoCanSeeProfilePhoto != "connections_only" ||
			!got.BlockUnknownCalls || !got.DmSafeMode || !got.DmSensitiveMediaFilter {
			t.Fatalf("minor profile not applied at creation: %+v", got)
		}
		return got
	}

	t.Run("adult, user-service creates the row", func(t *testing.T) {
		id := uuid.New()
		seedAuthUser(t, pool, id)
		if err := svc.CreateUser(ctx, id, false); err != nil {
			t.Fatalf("create: %v", err)
		}
		if r := readRow(t, pool, id); r.msg != "everyone_message_requests" || r.call != "accepted_chats_only" {
			t.Fatalf("adult defaults: got (%q, %q)", r.msg, r.call)
		}
	})

	t.Run("minor, user-service creates the row", func(t *testing.T) {
		id := uuid.New()
		seedAuthUser(t, pool, id)
		if err := svc.CreateUser(ctx, id, true); err != nil {
			t.Fatalf("create: %v", err)
		}
		assertMinor(t, id)
	})

	t.Run("minor, auth-service already inserted adult defaults", func(t *testing.T) {
		id := uuid.New()
		seedAuthUser(t, pool, id)
		// What auth-service's CreateUserRecordTx writes in the registration
		// transaction: column defaults only.
		if _, err := pool.Exec(ctx, `INSERT INTO usr.users (id) VALUES ($1)`, id); err != nil {
			t.Fatalf("seed usr user: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO usr.user_settings (user_id, account_visibility, allow_messages_from, allow_comments_from)
			VALUES ($1, 'public', 'everyone', 'everyone')`, id); err != nil {
			t.Fatalf("seed auth-service settings row: %v", err)
		}
		if err := svc.CreateUser(ctx, id, true); err != nil {
			t.Fatalf("create: %v", err)
		}
		first := assertMinor(t, id)

		// Redelivered event: nothing to change, nothing written.
		if err := svc.CreateUser(ctx, id, true); err != nil {
			t.Fatalf("redelivered create: %v", err)
		}
		second := assertMinor(t, id)
		if second.PrivacyVersion != first.PrivacyVersion {
			t.Fatalf("redelivery bumped privacy_version %d -> %d", first.PrivacyVersion, second.PrivacyVersion)
		}

		// The minor's later write of an open value is clamped.
		second.WhoCanMessage = "everyone_message_requests"
		saved, err := svc.UpdateSettings(ctx, second)
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if saved.WhoCanMessage != "people_i_follow" {
			t.Fatalf("minor write stored %q, want people_i_follow", saved.WhoCanMessage)
		}
	})
}

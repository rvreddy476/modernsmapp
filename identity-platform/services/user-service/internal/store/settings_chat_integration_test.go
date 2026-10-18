//go:build integration

package store

// Live PostgreSQL test for the chat-parity settings columns (CONTRACT §7).
//
//	USER_SETTINGS_IT_POSTGRES_DSN=postgres://.../user_settings_chat_it_test \
//	  go test -tags integration ./internal/store/ -run Integration
//
// Applies the REAL auth-service setup.sql (which owns usr.user_settings) and
// then this service's embedded setup.sql TWICE, the way two boots would.
// Refuses any database whose name does not end in "_test".

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/atpost/identity-shared/store/schemabootstrap"
	"github.com/atpost/identity-shared/store/schemaguard"
	"github.com/atpost/identity-user-service/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func chatSettingsPool(t *testing.T) *pgxpool.Pool {
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
	return pool
}

func TestIntegrationChatSettingsMigrationIdempotent(t *testing.T) {
	pool := chatSettingsPool(t)
	ctx := context.Background()

	authSQL, err := os.ReadFile(filepath.Join("..", "..", "..", "auth-service", "database", "setup.sql"))
	if err != nil {
		t.Fatalf("read auth-service setup.sql: %v", err)
	}
	if err := schemabootstrap.Apply(ctx, pool, string(authSQL)); err != nil {
		t.Fatalf("auth-service schema: %v", err)
	}

	// A row that exists BEFORE the new columns: it must pick up the defaults.
	legacy := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO auth.users (user_id, email) VALUES ($1, $2)`,
		legacy, legacy.String()+"@example.test"); err != nil {
		t.Fatalf("seed auth user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO usr.users (id) VALUES ($1)`, legacy); err != nil {
		t.Fatalf("seed usr user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO usr.user_settings (user_id) VALUES ($1)`, legacy); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	for i := 1; i <= 2; i++ {
		if err := schemabootstrap.Apply(ctx, pool, database.SetupSQL); err != nil {
			t.Fatalf("user-service setup.sql run %d: %v", i, err)
		}
	}
	if err := schemaguard.Verify(ctx, pool, "user-service", SchemaRequirements); err != nil {
		t.Fatalf("schema guard after two runs: %v", err)
	}

	st := New(pool)
	got, err := st.GetSettings(ctx, legacy)
	if err != nil || got == nil {
		t.Fatalf("get legacy settings: %v", err)
	}
	if !got.DmSafeMode || got.DmSensitiveMediaFilter || got.ChatBubbleStyle != "default" ||
		got.DmFilteredKeywords == nil || len(got.DmFilteredKeywords) != 0 {
		t.Fatalf("pre-existing row defaults wrong: %+v", got)
	}

	// Round trip through the real UPDATE.
	got.DmSafeMode = false
	got.DmSensitiveMediaFilter = true
	got.DmFilteredKeywords = []string{"crypto", "free money"}
	got.ChatBubbleStyle = "forest"
	updated, err := st.UpdateSettings(ctx, got)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	reread, err := st.GetSettings(ctx, legacy)
	if err != nil {
		t.Fatalf("reread: %v", err)
	}
	for _, s := range []*UserSettings{updated, reread} {
		if s.DmSafeMode || !s.DmSensitiveMediaFilter || s.ChatBubbleStyle != "forest" ||
			!reflect.DeepEqual(s.DmFilteredKeywords, []string{"crypto", "free money"}) {
			t.Fatalf("round trip wrong: %+v", s)
		}
	}

	// Backstop CHECKs refuse writes that bypass the service.
	if _, err := pool.Exec(ctx, `UPDATE usr.user_settings SET chat_bubble_style = 'sunset' WHERE user_id = $1`, legacy); err == nil {
		t.Fatal("CHECK should refuse an unknown chat_bubble_style")
	}
	tooMany := make([]string, 101)
	for i := range tooMany {
		tooMany[i] = "w"
	}
	if _, err := pool.Exec(ctx, `UPDATE usr.user_settings SET dm_filtered_keywords = $2 WHERE user_id = $1`, legacy, tooMany); err == nil {
		t.Fatal("CHECK should refuse more than 100 keywords")
	}

	// A user created after the migration gets the same defaults.
	fresh := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO auth.users (user_id, email) VALUES ($1, $2)`,
		fresh, fresh.String()+"@example.test"); err != nil {
		t.Fatalf("seed fresh auth user: %v", err)
	}
	if err := st.CreateUser(ctx, fresh, false); err != nil {
		t.Fatalf("create user: %v", err)
	}
	fs, err := st.GetSettings(ctx, fresh)
	if err != nil || fs == nil || !fs.DmSafeMode || fs.ChatBubbleStyle != "default" || len(fs.DmFilteredKeywords) != 0 {
		t.Fatalf("fresh user defaults wrong: %+v err=%v", fs, err)
	}
}

package database

import (
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// US-L5: the schema is setup.sql plus migrations/, nothing else. Until
// 2026-11-07 cmd/server/main.go also ran DDL on every boot, and some objects
// existed only there. This list is every table, index and column that boot
// DDL created; each must now be declared in this folder, or a fresh database
// comes up without it.
var bootDDLObjects = struct{ tables, indexes, columns []string }{
	tables: []string{
		"channels", "channel_links", "channel_milestones", "channel_members",
		"business_pages", "business_reviews", "page_followers",
		"user_reputation", "endorsements", "handles", "handle_history",
		"profile_pins", "portfolio_items", "profile_qr_codes", "digital_wellbeing", "screen_time_log",
		"projection_sync_checkpoint", "consumer_dlq",
	},
	indexes: []string{
		"idx_channels_user", "idx_channels_handle", "idx_channel_links_channel", "idx_channel_milestones_channel",
		"idx_business_pages_user", "idx_business_pages_handle", "idx_business_pages_category", "idx_business_pages_seller",
		"idx_business_reviews_page", "idx_page_followers_user", "idx_endorsements_to", "idx_endorsements_from",
		"idx_handles_owner", "idx_handle_history_handle", "idx_portfolio_user", "idx_screen_time_user",
		"idx_consumer_dlq_unreplayed",
	},
	columns: []string{
		// users
		"pronouns", "status_text", "status_emoji", "status_expires_at", "handle",
		// user_links
		"click_count",
		// channels
		"is_default",
		// business_pages
		"follower_count", "cover_media_id", "avatar_media_id", "website", "status", "seller_id",
	},
}

func schemaSQL(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	b.WriteString(SetupSQL)
	names, err := fs.Glob(Migrations, "migrations/*.sql")
	if err != nil || len(names) == 0 {
		t.Fatalf("no embedded migrations: %v", err)
	}
	sort.Strings(names)
	for _, n := range names {
		body, err := fs.ReadFile(Migrations, n)
		if err != nil {
			t.Fatal(err)
		}
		b.WriteString("\n")
		b.Write(body)
	}
	return b.String()
}

func TestBootDDLObjectsAreDeclaredInTheSchema(t *testing.T) {
	sql := schemaSQL(t)
	declared := func(pattern string) bool { return regexp.MustCompile(`(?m)` + pattern).MatchString(sql) }
	for _, name := range bootDDLObjects.tables {
		if !declared(`CREATE TABLE IF NOT EXISTS ` + name + `\s*\(`) {
			t.Errorf("table %s is not created by setup.sql or a migration", name)
		}
	}
	for _, name := range bootDDLObjects.indexes {
		if !declared(`CREATE (UNIQUE )?INDEX IF NOT EXISTS ` + name + `\b`) {
			t.Errorf("index %s is not created by setup.sql or a migration", name)
		}
	}
	for _, name := range bootDDLObjects.columns {
		if !declared(`ADD COLUMN IF NOT EXISTS `+name+`\b`) && !declared(`^\s+`+name+`\s+[A-Z]`) {
			t.Errorf("column %s is not declared by setup.sql or a migration", name)
		}
	}
}

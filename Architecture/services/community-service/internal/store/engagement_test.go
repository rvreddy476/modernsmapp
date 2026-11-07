package store

import (
	"strings"
	"testing"
	"time"
)

/*
A spark counts once per user and post, and a view once per user and post per
ViewDedupWindow. Before, the insert was ON CONFLICT DO NOTHING but the count
UPDATE ran on every call, so one user could push either count up without
limit. The count now moves in the same statement as the insert, and only
when the insert (or, for a view, the window refresh) returned a row.

These pin the statement shape without a database; the behaviour is proved
against Postgres in engagement_integration_test.go (CI).
*/

func squash(s string) string { return strings.Join(strings.Fields(s), " ") }

func TestSparkCountsOncePerUser(t *testing.T) {
	sql := squash(sparkPostSQL)
	for _, must := range []string{
		"ON CONFLICT (post_id, user_id) DO NOTHING RETURNING 1",
		"UPDATE community_posts SET spark_count = spark_count + $4",
		"WHERE id = $1 AND EXISTS (SELECT 1 FROM spark)",
	} {
		if !strings.Contains(sql, must) {
			t.Errorf("sparkPostSQL lost %q:\n%s", must, sql)
		}
	}
	fn := squash(storeFunc(t, "community_posts.go", "SparkCommunityPost"))
	if !strings.Contains(fn, "s.db.Exec(ctx, sparkPostSQL, postID, userID, isSupernova, weight)") ||
		strings.Count(fn, "s.db.Exec(") != 1 {
		t.Fatalf("SparkCommunityPost must run the one counted statement only:\n%s", fn)
	}
}

func TestViewCountsOncePerWindow(t *testing.T) {
	sql := squash(recordViewSQL)
	for _, must := range []string{
		"ON CONFLICT (post_id, user_id) DO UPDATE SET viewed_at = NOW()",
		"WHERE community_post_views.viewed_at IS NULL OR community_post_views.viewed_at < NOW() - make_interval(secs => $3)",
		"RETURNING 1",
		"UPDATE community_posts SET view_count = view_count + 1",
		"WHERE id = $1 AND EXISTS (SELECT 1 FROM counted)",
	} {
		if !strings.Contains(sql, must) {
			t.Errorf("recordViewSQL lost %q:\n%s", must, sql)
		}
	}
	fn := squash(storeFunc(t, "community_posts.go", "RecordCommunityPostView"))
	if !strings.Contains(fn, "s.db.Exec(ctx, recordViewSQL, postID, userID, ViewDedupWindow.Seconds())") ||
		strings.Count(fn, "s.db.Exec(") != 1 {
		t.Fatalf("RecordCommunityPostView must run the one counted statement only:\n%s", fn)
	}
	if ViewDedupWindow < time.Hour {
		t.Fatalf("ViewDedupWindow = %v: too short to dedupe a reload loop", ViewDedupWindow)
	}
}

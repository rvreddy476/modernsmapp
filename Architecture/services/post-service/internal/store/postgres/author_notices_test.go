package postgres

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Report resolution (lane post-service-2): which decisions tell the author,
// and which actions each source may take.
func TestModerationNoticeKind(t *testing.T) {
	cases := []struct{ prev, next, want string }{
		{"approved", "rejected", AuthorNoticeModerationTakedown},
		{"pending", "rejected", AuthorNoticeModerationTakedown},
		{"flagged", "rejected", AuthorNoticeModerationTakedown},
		{"rejected", "approved", AuthorNoticeModerationRestored},
		{"rejected", "rejected", ""},
		{"pending", "approved", ""},
		{"flagged", "needs_changes", ""},
		{"rejected", "needs_changes", ""},
	}
	for _, c := range cases {
		if got := moderationNoticeKind(c.prev, c.next); got != c.want {
			t.Errorf("%s -> %s: notice %q, want %q", c.prev, c.next, got, c.want)
		}
	}
}

func TestModerationSourceAllowed(t *testing.T) {
	for _, action := range []string{"approve", "reject", "needs_changes"} {
		if !moderationSourceAllowed("admin", action) || !moderationSourceAllowed("appeal", action) {
			t.Errorf("admin/appeal must take %s", action)
		}
	}
	if !moderationSourceAllowed("report", "reject") {
		t.Error("an upheld report must take the post down")
	}
	for _, action := range []string{"approve", "needs_changes"} {
		if moderationSourceAllowed("report", action) {
			t.Errorf("a report must not %s", action)
		}
	}
	for _, src := range []string{"", "copyright", "REPORT", "user"} {
		if moderationSourceAllowed(src, "reject") {
			t.Errorf("unknown source %q accepted", src)
		}
	}
}

func TestScheduledVideoFailedNoticeNamesTheCreatorAndWhy(t *testing.T) {
	post, author := uuid.New(), uuid.New()
	n := scheduledVideoFailedNotice(post, author, "long_video", "My film", time.Now())
	if n.Kind != AuthorNoticeScheduledVideoFailed || n.NoticeID != post.String() ||
		n.PostID != post.String() || n.AuthorID != author.String() || n.Title != "My film" {
		t.Fatalf("notice = %+v", n)
	}
	if !strings.Contains(n.Reason, "could not be processed") || n.Appealable {
		t.Fatalf("notice reason/appealable = %q/%v", n.Reason, n.Appealable)
	}
}

// The failed-video rule names the failed upload, the schedule and a live,
// non-note post: dropping any of the four would take the wrong posts off
// the schedule.
func TestFailedScheduledVideoSQLNamesEveryCondition(t *testing.T) {
	sql := failedScheduledVideoSQL("p")
	for _, want := range []string{
		"p.publish_at IS NOT NULL",
		"p.deleted_at IS NULL",
		NotNoteSQL("p"),
		"vm.upload_status = 'failed'",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("failedScheduledVideoSQL lacks %q:\n%s", want, sql)
		}
	}
}

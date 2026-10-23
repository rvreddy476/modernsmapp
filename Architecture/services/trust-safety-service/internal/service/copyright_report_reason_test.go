package service

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// copyright_mine ("Copyright: this is my content") and copyright_media
// ("Copyright: film, TV or music") are report reasons on posts only
// (videos and reels alias to post). Validation runs before the store
// (New(nil, nil) would panic past it), so a refusal here is proof nothing
// was written.
func TestCopyrightReportReasonsOnPostsOnly(t *testing.T) {
	t.Parallel()
	svc := New(nil, nil)
	reporter := uuid.New()
	for _, reason := range []string{ReasonCopyrightMine, ReasonCopyrightMedia} {
		if !validReportCategories[reason] {
			t.Fatalf("%s is not an accepted report reason", reason)
		}
		if CopyrightReasonLabels[reason] == "" {
			t.Fatalf("%s has no label", reason)
		}
		for _, typ := range []string{"user", "comment", "hashtag", "sound", "message", "conversation"} {
			_, err := svc.FileReportRef(context.Background(), reporter, typ, uuid.NewString(), reason, "")
			if !errors.Is(err, ErrInvalidReport) || !strings.Contains(err.Error(), "posts (videos and reels) only") {
				t.Errorf("%s + %s: err = %v, want ErrInvalidReport naming posts", typ, reason, err)
			}
		}
		// Posts and their reel / video aliases pass the reason checks: a
		// malformed id is what stops them, so the store is never hit.
		for _, typ := range []string{"post", "reel", "video"} {
			_, err := svc.FileReportRef(context.Background(), reporter, typ, "not-an-id", strings.ToUpper(reason), "")
			if !errors.Is(err, ErrInvalidReport) || strings.Contains(err.Error(), "only") || strings.Contains(err.Error(), "invalid reason") {
				t.Errorf("%s + %s: err = %v, want only the id refused", typ, reason, err)
			}
		}
	}
	if CopyrightReasonLabels[ReasonCopyrightMine] != "Copyright: this is my content" ||
		CopyrightReasonLabels[ReasonCopyrightMedia] != "Copyright: film, TV or music" {
		t.Fatal("labels drifted from the founder's wording")
	}
}

// The database CHECK admits both reasons (migration 018), and keeps every
// earlier one.
func TestCopyrightReportReasonsMigration(t *testing.T) {
	raw, err := os.ReadFile("../../database/migrations/018_copyright_report_reasons.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for reason := range validReportCategories {
		if !strings.Contains(sql, "'"+reason+"'") {
			t.Errorf("migration 018's CHECK does not admit %q", reason)
		}
	}
}

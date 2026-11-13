package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// impersonation (short-posts design 3.4) is a report reason on profiles and
// posts only. Validation runs before the store (New(nil, nil) would panic
// past it), so a refusal here is proof nothing was written.
func TestImpersonationReasonOnProfilesAndPostsOnly(t *testing.T) {
	t.Parallel()
	svc := New(nil, nil)
	reporter := uuid.New()
	const wrongTarget = "profiles and posts only"

	if !validReportCategories[ReasonImpersonation] {
		t.Fatal("impersonation is not an accepted report reason")
	}
	for _, typ := range []string{"comment", "hashtag", "sound", "message", "conversation"} {
		_, err := svc.FileReportRef(context.Background(), reporter, typ, uuid.NewString(), "impersonation", "")
		if !errors.Is(err, ErrInvalidReport) || !strings.Contains(err.Error(), wrongTarget) {
			t.Errorf("%s + impersonation: err = %v, want ErrInvalidReport naming the targets", typ, err)
		}
	}
	// Profiles, posts and the post aliases pass the reason checks: only the
	// malformed id stops them, so the store is never reached.
	for _, typ := range []string{"user", "post", "reel", "video"} {
		_, err := svc.FileReportRef(context.Background(), reporter, typ, "not-an-id", "IMPERSONATION", "")
		if !errors.Is(err, ErrInvalidReport) || strings.Contains(err.Error(), wrongTarget) || strings.Contains(err.Error(), "invalid reason") {
			t.Errorf("%s + impersonation: err = %v, want only the id refused", typ, err)
		}
	}
}

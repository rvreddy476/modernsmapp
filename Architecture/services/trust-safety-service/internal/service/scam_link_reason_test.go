package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// scam_link ("Fake job or scam link", Action button contract 23 Oct 2026)
// is a report reason on posts and profiles only. Validation runs before the
// store (New(nil, nil) would panic past it), so a refusal here is proof
// nothing was written.
func TestScamLinkReasonOnPostsAndProfilesOnly(t *testing.T) {
	t.Parallel()
	svc := New(nil, nil)
	reporter := uuid.New()
	const wrongTarget = "posts and profiles only"

	if !validReportCategories[ReasonScamLink] {
		t.Fatal("scam_link is not an accepted report reason")
	}
	// Other targets: refused for the reason, before the id is even read.
	for _, typ := range []string{"comment", "hashtag", "sound", "message", "conversation"} {
		_, err := svc.FileReportRef(context.Background(), reporter, typ, uuid.NewString(), "scam_link", "")
		if !errors.Is(err, ErrInvalidReport) || !strings.Contains(err.Error(), wrongTarget) {
			t.Errorf("%s + scam_link: err = %v, want ErrInvalidReport naming the targets", typ, err)
		}
	}
	// Posts (and their reel / video aliases) and profiles pass the reason
	// checks: a malformed id is what stops them, so the store is never hit.
	for _, typ := range []string{"post", "reel", "video", "user"} {
		_, err := svc.FileReportRef(context.Background(), reporter, typ, "not-an-id", "SCAM_LINK", "")
		if !errors.Is(err, ErrInvalidReport) || strings.Contains(err.Error(), wrongTarget) || strings.Contains(err.Error(), "invalid reason") {
			t.Errorf("%s + scam_link: err = %v, want only the id refused", typ, err)
		}
	}
}

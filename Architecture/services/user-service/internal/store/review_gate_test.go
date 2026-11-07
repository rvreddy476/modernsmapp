package store

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

// US-L3: a page takes reviews only while approved, and never from its owner
// or from an owner/admin on its roster.
func TestReviewRefusal(t *testing.T) {
	owner, stranger := uuid.New(), uuid.New()
	for _, tc := range []struct {
		name     string
		status   string
		reviewer uuid.UUID
		manages  bool
		want     error
	}{
		{"a stranger on an approved page", "approved", stranger, false, nil},
		{"the owner", "approved", owner, false, ErrCannotReviewOwn},
		{"an admin on the roster", "approved", stranger, true, ErrCannotReviewOwn},
		{"a draft page", "draft", stranger, false, ErrPageNotReviewable},
		{"a page in review", "pending_review", stranger, false, ErrPageNotReviewable},
		{"a rejected page", "rejected", stranger, false, ErrPageNotReviewable},
		{"a suspended page", "suspended", stranger, false, ErrPageNotReviewable},
		{"a disabled page", "disabled", stranger, false, ErrPageNotReviewable},
		{"the legacy active status", "active", stranger, false, ErrPageNotReviewable},
		{"the owner of a draft: the status answers first", "draft", owner, false, ErrPageNotReviewable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := reviewRefusal(tc.status, owner, tc.reviewer, tc.manages); !errors.Is(got, tc.want) || (tc.want == nil) != (got == nil) {
				t.Fatalf("reviewRefusal = %v, want %v", got, tc.want)
			}
		})
	}
}

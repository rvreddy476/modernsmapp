package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// Security sweep #20: a resumable init is an upload and counts against the
// per-user upload limits InitUpload applies. The limiter is reached BEFORE
// the object store or the database (both nil here: reaching either would
// panic), so a limited user opens no multipart session at all.
func TestResumableInitIsRateLimited(t *testing.T) {
	limited := errors.New("upload rate limit exceeded: max 10 uploads per hour")
	var asked []uuid.UUID
	s := &Service{uploadLimit: func(_ context.Context, userID uuid.UUID) error {
		asked = append(asked, userID)
		return limited
	}}
	user := uuid.New()

	_, err := s.InitResumableUpload(context.Background(), user, "video", "video/mp4", 100<<20)
	if !errors.Is(err, limited) {
		t.Fatalf("err = %v, want the limiter's refusal", err)
	}
	if len(asked) != 1 || asked[0] != user {
		t.Fatalf("limiter asked about %v, want exactly the uploader once", asked)
	}
}

// A request the validator refuses never reaches the limiter: a rejected
// upload must not spend the user's quota.
func TestResumableInitValidatesBeforeCounting(t *testing.T) {
	calls := 0
	s := &Service{uploadLimit: func(context.Context, uuid.UUID) error { calls++; return nil }}
	if _, err := s.InitResumableUpload(context.Background(), uuid.New(), "video", "text/html", 100); err == nil {
		t.Fatal("an HTML upload was accepted")
	}
	if calls != 0 {
		t.Fatalf("the limiter was charged %d time(s) for a refused upload", calls)
	}
}

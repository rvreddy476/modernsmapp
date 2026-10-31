package service

import (
	"context"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// The document budget against a real Redis protocol (miniredis): exactly
// MaxDocumentUploadsPerDay per user per day, its own counter, and neither
// budget spends the other.
func TestDocumentRateLimitCountsItsOwnBudget(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	s := &Service{rdb: rdb}
	ctx := context.Background()
	user, other := uuid.New(), uuid.New()

	// The media budget is spent first: 10 uploads this hour. Documents are
	// still allowed.
	for i := 0; i < MaxUploadsPerHour; i++ {
		if err := s.CheckUploadRateLimit(ctx, user); err != nil {
			t.Fatalf("media upload %d: %v", i+1, err)
		}
	}
	if err := s.CheckUploadRateLimit(ctx, user); err == nil {
		t.Fatal("control: the 11th media upload in an hour was allowed")
	}

	for i := 0; i < MaxDocumentUploadsPerDay; i++ {
		if err := s.CheckDocumentUploadRateLimit(ctx, user); err != nil {
			t.Fatalf("document upload %d of %d refused: %v", i+1, MaxDocumentUploadsPerDay, err)
		}
	}
	err := s.CheckDocumentUploadRateLimit(ctx, user)
	if err == nil || !strings.Contains(err.Error(), "upload rate limit exceeded") {
		t.Fatalf("document upload %d: %v, want the rate-limit refusal (429)", MaxDocumentUploadsPerDay+1, err)
	}
	// Per user.
	if err := s.CheckDocumentUploadRateLimit(ctx, other); err != nil {
		t.Fatalf("another user's first document was refused: %v", err)
	}
	// And the documents did not touch the media counters.
	if got, _ := mr.Get("upload:rate:" + other.String() + ":day"); got != "" {
		t.Fatalf("a document upload spent the media day counter (%s)", got)
	}
	if ttl := mr.TTL(documentDayKey(user)); ttl <= 0 || ttl.Hours() > 24 {
		t.Fatalf("the document counter's window is %v, want one day", ttl)
	}
}

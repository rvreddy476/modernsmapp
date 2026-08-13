package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestLegacyModerationAndSuspensionFailBeforeStoreMutation(t *testing.T) {
	svc := &Service{} // nil store makes any accidental mutation path panic.
	if err := svc.TakedownContent(context.Background(), uuid.NewString(), "post", uuid.NewString(), "reason"); !errors.Is(err, ErrCanonicalModerationRequired) {
		t.Fatalf("legacy takedown = %v", err)
	}
	if err := svc.SuspendUser(context.Background(), uuid.NewString(), uuid.New(), time.Now().Add(time.Hour), "reason"); !errors.Is(err, ErrSuspensionUnavailable) {
		t.Fatalf("legacy suspension = %v", err)
	}
	if err := svc.UnsuspendUser(context.Background(), uuid.NewString(), uuid.New()); !errors.Is(err, ErrSuspensionUnavailable) {
		t.Fatalf("legacy unsuspension = %v", err)
	}
}

package events

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// PublishPostCreated must refuse a zero createdAt rather than publish one.
//
// The parameter exists because this function used to stamp time.Now() itself,
// which no caller could override and every caller was silently wrong about:
// PostCreated is the ONLY writer of analytics.content_ownership, and that
// row's created_at is read by idx_content_ownership_creator — the index the
// creator fund's daily settlement and its 90-day eligibility scan walk. A
// misdated row misdates every earning derived from it, and nothing downstream
// can recover the true value, because the event is the only record that
// crosses the service boundary.
//
// A zero time is the failure mode a refactor actually produces: someone adds
// a caller, forgets the argument, and Go hands them the zero value happily. It
// would serialise as year 1 and park the content outside every window the fund
// queries — silently, and forever. Refusing is the only outcome a human sees.
//
// The producer's writer is nil here, so a call that got PAST the guard would
// panic rather than pass; the assertion is that it never gets there.
func TestPublishPostCreatedRefusesAZeroCreatedAt(t *testing.T) {
	p := &Producer{}
	err := p.PublishPostCreated(context.Background(), uuid.New(), uuid.New(),
		"text", "public", "flick", "approved", 0, time.Time{})
	if err == nil {
		t.Fatal("a zero createdAt was accepted; it would publish an event dated year 1")
	}
}

package events

import (
	"context"
	"testing"
	"time"

	"github.com/atpost/shared/events"
)

// Audit 2026-11-07, S-M1: both live post writers hand the author's privacy
// to the projection, which writes it over the document (a full replace
// would otherwise reset a private author's post to public).

type privateAuthors map[string]bool

func (p privateAuthors) IsPrivate(_ context.Context, id string) (bool, error) { return p[id], nil }
func (privateAuthors) Invalidate(string)                                      {}

func TestLivePostWriters_CarryTheAuthorsPrivacy(t *testing.T) {
	f := newFakeOS(t)
	c := newTestConsumer(t, f)
	c.privacy = privateAuthors{"author-1": true}
	ctx := context.Background()

	if err := c.processMessage(ctx, msg(t, events.PostCreated, events.PostCreatedPayload{
		PostID: "created", AuthorID: "author-1", Text: "hello",
		Visibility: "public", ReviewStatus: "approved", SearchRev: 1, CreatedAt: time.Now().UTC(),
	})); err != nil {
		t.Fatalf("PostCreated: %v", err)
	}
	if err := c.processMessage(ctx, eligibilityMsg(t, "eligible", "public", "approved", false, 2)); err != nil {
		t.Fatalf("eligibility: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range []string{"created", "eligible"} {
		if f.docs[id]["author_is_private"] != true {
			t.Fatalf("%s: author_is_private = %v; a private author's post was indexed as public", id, f.docs[id]["author_is_private"])
		}
	}
}

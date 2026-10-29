package mediaclaims

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
)

func TestAttachmentIDsStrictAndStored(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	mixed := `[{"media_id":"` + a.String() + `"},"not-a-uuid",{"url":"x"},"` + b.String() + `"]`
	if _, err := AttachmentIDs(json.RawMessage(mixed)); err == nil {
		t.Fatal("a writer accepted an attachment that names no media id")
	}
	if got := StoredAttachmentIDs(json.RawMessage(mixed)); !slices.Equal(got, []uuid.UUID{a, b}) {
		t.Fatalf("stored rows must keep the ids they do name: %v", got)
	}
	if _, err := AttachmentIDs(json.RawMessage(`{"media_id":"` + a.String() + `"}`)); err == nil {
		t.Fatal("an object is not a list")
	}
	if got := StoredAttachmentIDs(json.RawMessage(`{"x":1}`)); len(got) != 0 {
		t.Fatalf("a non-list holds nothing: %v", got)
	}
	for _, empty := range []string{``, `null`, `[]`, ` `} {
		ids, err := AttachmentIDs(json.RawMessage(empty))
		if err != nil || len(ids) != 0 {
			t.Fatalf("%q: %v %v", empty, ids, err)
		}
	}
	ids, err := AttachmentIDs(json.RawMessage(`["` + a.String() + `",{"media_id":"` + b.String() + `"}]`))
	if err != nil || !slices.Equal(ids, []uuid.UUID{a, b}) {
		t.Fatalf("both shapes: %v %v", ids, err)
	}
}

// Verify is the gate without the claim: it refuses what Attach refuses and
// claims nothing either way.
func TestVerifyChecksWithoutClaiming(t *testing.T) {
	me := uuid.New()
	mine, theirs := asset(me, "ready", "passed"), asset(uuid.New(), "ready", "passed")
	f := newFake(mine, theirs)
	g := New(f, f)
	ref := uuid.New()
	if err := g.Verify(context.Background(), me, []Change{{Kind: kind, Ref: ref, Add: []uuid.UUID{mine.ID}}}); err != nil {
		t.Fatalf("own asset: %v", err)
	}
	err := g.Verify(context.Background(), me, []Change{{Kind: kind, Ref: ref, Add: []uuid.UUID{mine.ID, theirs.ID}}})
	if !errors.Is(err, ErrMediaNotOwned) {
		t.Fatalf("someone else's asset = %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("Verify called the claim API: %v", f.calls)
	}
	var none *Gate
	if err := none.Verify(context.Background(), me, []Change{{Kind: kind, Ref: ref, Add: []uuid.UUID{mine.ID}}}); !errors.Is(err, ErrMediaUnavailable) {
		t.Fatalf("no gate = %v", err)
	}
}

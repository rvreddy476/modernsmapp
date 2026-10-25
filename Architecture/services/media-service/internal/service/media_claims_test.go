package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

func goodClaim() postgres.MediaClaim {
	return postgres.MediaClaim{Claimant: "channel-service", Kind: "channel_update", Ref: uuid.NewString()}
}

func TestValidateClaimAcceptsEveryRegisteredKind(t *testing.T) {
	for claimant, kinds := range claimKinds {
		for kind := range kinds {
			c := postgres.MediaClaim{Claimant: claimant, Kind: kind, Ref: uuid.NewString()}
			if err := validateClaim(c, []uuid.UUID{uuid.New()}, false); err != nil {
				t.Errorf("%s/%s refused: %v", claimant, kind, err)
			}
		}
	}
	if err := validateClaim(goodClaim(), nil, true); err != nil {
		t.Errorf("a release of everything a holder claims must be allowed: %v", err)
	}
}

func TestValidateClaimRefusesWhatNobodyCouldExplain(t *testing.T) {
	tooMany := make([]uuid.UUID, maxClaimIDs+1)
	for i := range tooMany {
		tooMany[i] = uuid.New()
	}
	for name, tc := range map[string]struct {
		mutate func(*postgres.MediaClaim)
		ids    []uuid.UUID
	}{
		"unknown claimant": {func(c *postgres.MediaClaim) { c.Claimant = "post-service" }, []uuid.UUID{uuid.New()}},
		"unknown kind":     {func(c *postgres.MediaClaim) { c.Kind = "channel_story" }, []uuid.UUID{uuid.New()}},
		"empty ref":        {func(c *postgres.MediaClaim) { c.Ref = "  " }, []uuid.UUID{uuid.New()}},
		"long ref":         {func(c *postgres.MediaClaim) { c.Ref = strings.Repeat("x", maxClaimRefLen+1) }, []uuid.UUID{uuid.New()}},
		"no ids":           {func(*postgres.MediaClaim) {}, nil},
		"nil id":           {func(*postgres.MediaClaim) {}, []uuid.UUID{uuid.New(), uuid.Nil}},
		"too many ids":     {func(*postgres.MediaClaim) {}, tooMany},
	} {
		c := goodClaim()
		tc.mutate(&c)
		if err := validateClaim(c, tc.ids, false); !errors.Is(err, ErrClaimInvalid) {
			t.Errorf("%s: got %v, want ErrClaimInvalid", name, err)
		}
	}
	// A release is held to the same claimant/kind/ref rules.
	c := goodClaim()
	c.Claimant = "nobody"
	if err := validateClaim(c, nil, true); !errors.Is(err, ErrClaimInvalid) {
		t.Errorf("release by an unknown claimant: got %v", err)
	}
}

// A store "not found" is the claimant's signal not to write its row.
func TestClaimStoreErrorMapsMissingMedia(t *testing.T) {
	err := claimStoreError(fmt.Errorf("%w: %s", postgres.ErrMediaNotFound, uuid.New()))
	if !errors.Is(err, ErrClaimMediaNotFound) {
		t.Fatalf("got %v, want ErrClaimMediaNotFound", err)
	}
	other := errors.New("connection reset")
	if err := claimStoreError(other); errors.Is(err, ErrClaimMediaNotFound) || !errors.Is(err, other) {
		t.Fatalf("an infrastructure failure must not read as a verdict: %v", err)
	}
	if claimStoreError(nil) != nil {
		t.Fatal("nil must stay nil")
	}
}

func TestOwnerDeleteStoreErrorKeepsTheRefusal(t *testing.T) {
	inUse := &postgres.MediaInUseError{Holders: []postgres.ClaimHolder{{Claimant: "channel-service", Kind: "channel_update", Count: 2}}}
	var got *postgres.MediaInUseError
	if err := ownerDeleteStoreError(inUse); !errors.As(err, &got) || got != inUse {
		t.Fatalf("the in-use refusal must reach the handler whole, got %v", err)
	}
	// Passed through as itself: it is a decision, not a failed delete, and
	// must not be logged or reported as "delete media record: ...".
	if err := ownerDeleteStoreError(inUse); err != error(inUse) {
		t.Fatalf("the in-use refusal was wrapped as a failure: %v", err)
	}
	if err := ownerDeleteStoreError(fmt.Errorf("%w: foreign key", postgres.ErrMediaStillReferenced)); !errors.Is(err, ErrMediaReferenced) {
		t.Fatalf("a foreign-key refusal must be ErrMediaReferenced, got %v", err)
	}
	if err := ownerDeleteStoreError(postgres.ErrMediaNotFound); err == nil || err.Error() != "media not found" {
		t.Fatalf("a vanished row keeps the handler's not-found message, got %v", err)
	}
	if err := ownerDeleteStoreError(errors.New("boom")); err == nil || errors.Is(err, ErrMediaReferenced) {
		t.Fatalf("an unknown failure is a plain error, got %v", err)
	}
	if ownerDeleteStoreError(nil) != nil {
		t.Fatal("success must stay success")
	}
}

func TestMediaInUseMessageSaysWhereAndWhatToDo(t *testing.T) {
	msg := MediaInUseMessage([]postgres.ClaimHolder{
		{Claimant: "channel-service", Kind: "channel_avatar", Count: 1},
		{Claimant: "channel-service", Kind: "channel_update", Count: 3},
	})
	want := "This media is still used in 1 channel picture and 3 channel updates. Remove it there first, then delete it."
	if msg != want {
		t.Fatalf("got  %q\nwant %q", msg, want)
	}
	if got := MediaInUseMessage([]postgres.ClaimHolder{{Claimant: "x", Kind: "y", Count: 2}}); !strings.Contains(got, "2 other places") {
		t.Fatalf("an unlabelled holder still reads as a place: %q", got)
	}
	if got := MediaInUseMessage(nil); !strings.Contains(got, "still in use") {
		t.Fatalf("no holders: %q", got)
	}
}

func TestMediaInUseDetailsNameNoRows(t *testing.T) {
	d := MediaInUseDetails([]postgres.ClaimHolder{{Claimant: "channel-service", Kind: "channel_update", Count: 2}})
	if len(d) != 1 || d[0] != (InUseDetail{Service: "channel-service", Kind: "channel_update", Count: 2}) {
		t.Fatalf("got %+v", d)
	}
}

// "Keep nothing" through the except path would release everything on a
// client bug that dropped the list; it must be a plain release instead.
func TestReleaseExceptRefusesAnEmptyKeep(t *testing.T) {
	s := &Service{}
	if _, err := s.ReleaseMediaClaimsExcept(context.Background(), goodClaim(), nil); !errors.Is(err, ErrClaimInvalid) {
		t.Fatalf("got %v, want ErrClaimInvalid", err)
	}
}

// DeleteMedia needs PostgreSQL; its wiring is pinned on the source (the
// behaviour is TestLiveMediaClaimHTTPContract in CI): the store's answer goes
// through ownerDeleteStoreError before any blob is touched.
func TestOwnerDeleteRoutesTheStoreAnswer(t *testing.T) {
	raw, err := os.ReadFile("media.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	i := strings.Index(src, "func (s *Service) DeleteMedia(")
	if i < 0 {
		t.Fatal("DeleteMedia not found")
	}
	body := src[i:]
	if j := strings.Index(body[1:], "\nfunc "); j >= 0 {
		body = body[:j+1]
	}
	m, b := strings.Index(body, "if err := ownerDeleteStoreError(err); err != nil {\n\t\treturn err\n\t}"), strings.Index(body, "s.blobStore.DeleteObject(")
	if m < 0 || b < 0 || m > b {
		t.Fatalf("DeleteMedia must return ownerDeleteStoreError(err) before deleting blobs (at %d, %d)", m, b)
	}
}

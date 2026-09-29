// Copyright hold transitions without a database: the decision id is
// minted once, before the store is asked to persist it, with the digest
// post-service will see; the reason table is closed; only a human actor
// may transition; the dispatcher is kicked after a commit and never after
// a failure. The transaction itself is proven in
// copyright_integration_test.go.
package service

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/atpost/trust-safety-service/internal/restriction"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

// recordingCopyrightStore remembers what the service asked it to persist.
type recordingCopyrightStore struct {
	cases   map[uuid.UUID]*postgres.CopyrightCase
	created []postgres.NewCommand
	moved   []postgres.NewCommand
	fail    error
}

func newRecordingStore() *recordingCopyrightStore {
	return &recordingCopyrightStore{cases: map[uuid.UUID]*postgres.CopyrightCase{}}
}

func (r *recordingCopyrightStore) CreateCaseAndPlaceHold(_ context.Context, in postgres.CreateCaseInput, cmd postgres.NewCommand, meta postgres.AuditMeta) (*postgres.CopyrightCase, *postgres.RestrictionCommand, error) {
	if r.fail != nil {
		return nil, nil, r.fail
	}
	r.created = append(r.created, cmd)
	c := &postgres.CopyrightCase{ID: in.CaseID, SubjectPostID: in.SubjectPostID, SubjectAuthorID: in.SubjectAuthorID,
		Source: postgres.CopyrightSource, State: postgres.CopyrightCaseHoldActive, CaseRevision: 1, PolicyVersion: in.PolicyVersion}
	r.cases[in.CaseID] = c
	return c, &postgres.RestrictionCommand{DecisionID: cmd.DecisionID, CaseID: c.ID, CaseRevision: 1, ClaimsDigest: cmd.ClaimsDigest, ExpectedState: cmd.ExpectedState}, nil
}

func (r *recordingCopyrightStore) TransitionHold(_ context.Context, caseID uuid.UUID, cmd postgres.NewCommand, meta postgres.AuditMeta) (*postgres.CopyrightCase, *postgres.RestrictionCommand, error) {
	if r.fail != nil {
		return nil, nil, r.fail
	}
	c, ok := r.cases[caseID]
	if !ok {
		return nil, nil, postgres.ErrCopyrightCaseNotFound
	}
	r.moved = append(r.moved, cmd)
	c.CaseRevision++
	if cmd.Action == postgres.RestrictionActionPlaceHold {
		c.State = postgres.CopyrightCaseHoldActive
	} else {
		c.State = postgres.CopyrightCaseHoldReleased
	}
	return c, &postgres.RestrictionCommand{DecisionID: cmd.DecisionID, CaseID: c.ID, CaseRevision: c.CaseRevision, ClaimsDigest: cmd.ClaimsDigest, ExpectedState: cmd.ExpectedState}, nil
}

func (r *recordingCopyrightStore) GetCase(_ context.Context, caseID uuid.UUID) (*postgres.CopyrightCase, *postgres.RestrictionCommand, error) {
	c, ok := r.cases[caseID]
	if !ok {
		return nil, nil, postgres.ErrCopyrightCaseNotFound
	}
	return c, nil, nil
}

func newCopyrightTestService(store *recordingCopyrightStore) (*Service, *int) {
	svc := New(nil, nil)
	svc.SetCopyrightStore(store)
	kicks := 0
	svc.SetRestrictionKick(func() { kicks++ })
	return svc, &kicks
}

func TestCopyright_DecisionIsMintedOnceWithThePostServiceDigest(t *testing.T) {
	store := newRecordingStore()
	svc, kicks := newCopyrightTestService(store)
	ctx := context.Background()
	admin, post, author := uuid.New(), uuid.New(), uuid.New()

	hold, err := svc.CreateCopyrightHold(ctx, CreateCopyrightHoldInput{SubjectPostID: post, SubjectAuthorID: author, ReasonCode: " removal_upheld "}, holdMeta(admin, ""))
	if err != nil {
		t.Fatal(err)
	}
	if len(store.created) != 1 || *kicks != 1 {
		t.Fatalf("created=%d kicks=%d", len(store.created), *kicks)
	}
	cmd := store.created[0]
	if cmd.DecisionID == uuid.Nil || cmd.DecisionID != hold.Enforcement.DecisionID {
		t.Fatalf("decision id must be minted before the store is called and returned: %v / %v", cmd.DecisionID, hold.Enforcement.DecisionID)
	}
	if cmd.Action != postgres.RestrictionActionPlaceHold || cmd.ExpectedState != postgres.RestrictionExpectedAbsent ||
		cmd.ReasonCode != "removal_upheld" || cmd.ActorID != admin {
		t.Fatalf("first place: %+v", cmd)
	}
	// The stored digest is exactly the digest post-service computes over
	// the signed claims for this row.
	want := restriction.Digest(restriction.Command{
		DecisionID: cmd.DecisionID, CaseID: hold.Case.ID, CaseRevision: 1,
		Action: postgres.RestrictionActionPlaceHold, Source: postgres.CopyrightSource,
		SubjectPostID: post, SubjectAuthorID: author, ExpectedState: postgres.RestrictionExpectedAbsent,
		ReasonCode: "removal_upheld", PolicyVersion: postgres.CopyrightPolicyVersion, ActorID: admin,
	})
	if !bytes.Equal(cmd.ClaimsDigest, want) {
		t.Fatalf("digest differs from what the dispatcher will sign")
	}

	// Release: a NEW decision id, revision 2, expected active.
	released, err := svc.ReleaseHold(ctx, hold.Case.ID, "claim_withdrawn", holdMeta(admin, ""))
	if err != nil {
		t.Fatal(err)
	}
	rel := store.moved[0]
	if rel.DecisionID == uuid.Nil || rel.DecisionID == cmd.DecisionID || rel.ExpectedState != postgres.RestrictionExpectedActive ||
		rel.Action != postgres.RestrictionActionReleaseHold || released.Case.CaseRevision != 2 || *kicks != 2 {
		t.Fatalf("release: %+v rev=%d kicks=%d", rel, released.Case.CaseRevision, *kicks)
	}
	wantRel := restriction.Digest(restriction.Command{
		DecisionID: rel.DecisionID, CaseID: hold.Case.ID, CaseRevision: 2,
		Action: postgres.RestrictionActionReleaseHold, Source: postgres.CopyrightSource,
		SubjectPostID: post, SubjectAuthorID: author, ExpectedState: postgres.RestrictionExpectedActive,
		ReasonCode: "claim_withdrawn", PolicyVersion: postgres.CopyrightPolicyVersion, ActorID: admin,
	})
	if !bytes.Equal(rel.ClaimsDigest, wantRel) {
		t.Fatalf("release digest differs from what the dispatcher will sign")
	}

	// Re-place: expected released, revision 3.
	again, err := svc.PlaceHold(ctx, hold.Case.ID, "reinstated_on_review", holdMeta(admin, ""))
	if err != nil {
		t.Fatal(err)
	}
	if rp := store.moved[1]; rp.ExpectedState != postgres.RestrictionExpectedReleased || rp.DecisionID == rel.DecisionID || again.Case.CaseRevision != 3 || *kicks != 3 {
		t.Fatalf("re-place: %+v rev=%d kicks=%d", rp, again.Case.CaseRevision, *kicks)
	}
}

func TestCopyright_ReasonTableIsClosed(t *testing.T) {
	store := newRecordingStore()
	svc, kicks := newCopyrightTestService(store)
	ctx := context.Background()
	admin := uuid.New()
	meta := holdMeta(admin, "")
	for _, bad := range []string{"", "spam", "claim_withdrawn", "REMOVAL_UPHELD"} {
		if _, err := svc.CreateCopyrightHold(ctx, CreateCopyrightHoldInput{SubjectPostID: uuid.New(), SubjectAuthorID: uuid.New(), ReasonCode: bad}, meta); !errors.Is(err, ErrInvalidCopyrightReason) {
			t.Fatalf("create with %q: %v", bad, err)
		}
	}
	hold, err := svc.CreateCopyrightHold(ctx, CreateCopyrightHoldInput{SubjectPostID: uuid.New(), SubjectAuthorID: uuid.New(), ReasonCode: "removal_upheld"}, meta)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "removal_upheld", "reinstated_on_review", "other"} {
		if _, err := svc.ReleaseHold(ctx, hold.Case.ID, bad, meta); !errors.Is(err, ErrInvalidCopyrightReason) {
			t.Fatalf("release with %q: %v", bad, err)
		}
	}
	for _, bad := range []string{"claim_withdrawn", "rule75_restore"} {
		if _, err := svc.PlaceHold(ctx, hold.Case.ID, bad, meta); !errors.Is(err, ErrInvalidCopyrightReason) {
			t.Fatalf("place with %q: %v", bad, err)
		}
	}
	if len(store.moved) != 0 || *kicks != 1 {
		t.Fatalf("refused transitions must write and kick nothing: moved=%d kicks=%d", len(store.moved), *kicks)
	}
}

func TestCopyright_HumanActorAndIdsRequired(t *testing.T) {
	store := newRecordingStore()
	svc, kicks := newCopyrightTestService(store)
	ctx := context.Background()
	in := CreateCopyrightHoldInput{SubjectPostID: uuid.New(), SubjectAuthorID: uuid.New(), ReasonCode: "removal_upheld"}
	for name, meta := range map[string]postgres.AuditMeta{
		"none":    {},
		"service": {Actor: postgres.ServiceActor("post-service")},
		"nil":     {Actor: postgres.UserActor(uuid.Nil)},
	} {
		if _, err := svc.CreateCopyrightHold(ctx, in, meta); !errors.Is(err, ErrActorRequired) {
			t.Fatalf("%s actor: %v", name, err)
		}
		if _, err := svc.ReleaseHold(ctx, uuid.New(), "claim_withdrawn", meta); !errors.Is(err, ErrActorRequired) {
			t.Fatalf("%s actor release: %v", name, err)
		}
	}
	meta := holdMeta(uuid.New(), "")
	if _, err := svc.CreateCopyrightHold(ctx, CreateCopyrightHoldInput{SubjectAuthorID: uuid.New(), ReasonCode: "removal_upheld"}, meta); !errors.Is(err, ErrInvalidCopyrightCase) {
		t.Fatalf("no post: %v", err)
	}
	if _, err := svc.ReleaseHold(ctx, uuid.Nil, "claim_withdrawn", meta); !errors.Is(err, ErrInvalidCopyrightCase) {
		t.Fatalf("nil case: %v", err)
	}
	if _, err := svc.ReleaseHold(ctx, uuid.New(), "claim_withdrawn", meta); !errors.Is(err, postgres.ErrCopyrightCaseNotFound) {
		t.Fatalf("unknown case: %v", err)
	}
	if len(store.created) != 0 || len(store.moved) != 0 || *kicks != 0 {
		t.Fatalf("nothing may be written or kicked: %d %d %d", len(store.created), len(store.moved), *kicks)
	}
	// Without a store the service says so.
	if _, err := New(nil, nil).CreateCopyrightHold(ctx, in, meta); !errors.Is(err, ErrCopyrightUnavailable) {
		t.Fatalf("no store: %v", err)
	}
}

func TestCopyright_StoreFailureDoesNotKick(t *testing.T) {
	store := newRecordingStore()
	store.fail = errors.New("db down")
	svc, kicks := newCopyrightTestService(store)
	if _, err := svc.CreateCopyrightHold(context.Background(), CreateCopyrightHoldInput{SubjectPostID: uuid.New(), SubjectAuthorID: uuid.New(), ReasonCode: "removal_upheld"}, holdMeta(uuid.New(), "")); err == nil {
		t.Fatal("want the store's error")
	}
	if *kicks != 0 {
		t.Fatalf("a failed transaction must not kick the dispatcher")
	}
}

func TestCopyright_PinnedCaseIDIsHonoured(t *testing.T) {
	store := newRecordingStore()
	svc, _ := newCopyrightTestService(store)
	pinned := uuid.New()
	hold, err := svc.CreateCopyrightHold(context.Background(), CreateCopyrightHoldInput{CaseID: pinned, SubjectPostID: uuid.New(), SubjectAuthorID: uuid.New(), ReasonCode: "removal_upheld"}, holdMeta(uuid.New(), ""))
	if err != nil || hold.Case.ID != pinned {
		t.Fatalf("pinned: %v %v", hold, err)
	}
}

func holdMeta(id uuid.UUID, reason string) postgres.AuditMeta {
	return postgres.AuditMeta{Actor: postgres.UserActor(id), Reason: reason, RequestID: "req-" + id.String()[:8]}
}

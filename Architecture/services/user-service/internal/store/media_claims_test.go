package store

import (
	"strings"
	"testing"

	"github.com/atpost/user-service/internal/mediaclient"
	"github.com/google/uuid"
)

// The holder lock must WAIT (a try-lock would let a second edit skip the
// serialisation) and be transaction-scoped (freed by a crash or a dropped
// connection, never leaked by a missed unlock).
func TestMediaHolderLockWaitsAndIsTransactionScoped(t *testing.T) {
	if lockMediaHolderSQL != `SELECT pg_advisory_xact_lock($1)` {
		t.Fatalf("lock SQL changed to %q", lockMediaHolderSQL)
	}
}

func TestMediaHolderLockKeyIsNamespacedAndStable(t *testing.T) {
	ref := uuid.New()
	// Two separate calls (not one expression twice, staticcheck SA4000): the
	// key must not depend on anything but ref.
	first := mediaHolderLockKey(ref)
	again := mediaHolderLockKey(ref)
	if first != again {
		t.Fatal("the key is not stable")
	}
	if mediaHolderLockKey(ref) == mediaHolderLockKey(uuid.New()) {
		t.Fatal("two holders share a key")
	}
	// Not the raw id bits: another lock on the same id must not collide.
	var raw int64
	for _, b := range ref[:8] {
		raw = raw<<8 | int64(b)
	}
	if mediaHolderLockKey(ref) == raw {
		t.Fatal("the key is not namespaced")
	}
}

// A delete enqueues releases only for the caller's own row: a non-owner's
// refused delete must not queue (and so release) the owner's claims.
func TestDeleteEnqueuesAreOwnerBound(t *testing.T) {
	for _, set := range [][]pendingEnqueue{channelDeletePending, portfolioDeletePending} {
		for _, st := range set {
			if !strings.Contains(st.sel, "id = $2 AND user_id = $3") {
				t.Errorf("%s enqueue is not bound to the row and its owner: %s", st.kind, st.sel)
			}
		}
	}
	for _, st := range purgePending {
		if !strings.Contains(st.sel, "user_id = $2") {
			t.Errorf("%s purge enqueue is not bound to the purged user: %s", st.kind, st.sel)
		}
	}
}

// Every kind a writer claims is one the queue can settle and the purge
// enqueues: a kind missing from either is a claim nothing ever releases.
func TestEveryKindIsCoveredByPurgeAndRowLookup(t *testing.T) {
	inPurge := map[string]bool{}
	for _, st := range purgePending {
		inPurge[st.kind] = true
		if !strings.Contains(st.sel, "FROM "+st.table+" ") {
			t.Errorf("%s: probed table %q is not the one read: %s", st.kind, st.table, st.sel)
		}
	}
	for _, kind := range mediaclient.Kinds {
		if !inPurge[kind] {
			t.Errorf("account purge does not enqueue %s", kind)
		}
	}
}

func TestParsePageMediaID(t *testing.T) {
	id := uuid.New()
	for in, want := range map[string]struct{ ok, present bool }{
		"":                           {false, false},
		"   ":                        {false, false},
		id.String():                  {true, true},
		strings.ToUpper(id.String()): {true, true},
		"logo.png":                   {false, true},
		uuid.Nil.String():            {false, true},
	} {
		got, ok, present := ParsePageMediaID(in)
		if ok != want.ok || present != want.present || (ok && got != id) {
			t.Errorf("%q: got (%v,%v,%v)", in, got, ok, present)
		}
	}
}

// A disabled page is the only terminal state; its claims are released.
func TestPageLiveRule(t *testing.T) {
	if pageLiveSQL != `(status <> 'disabled')` {
		t.Fatalf("page live rule changed: %s", pageLiveSQL)
	}
	if !strings.Contains(pageDisablePending[0].sel, "avatar_media_id <> ''") || !strings.Contains(pageDisablePending[1].sel, "cover_media_id <> ''") {
		t.Fatal("page disable enqueue does not cover avatar and cover")
	}
}

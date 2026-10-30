//go:build integration

package service

import (
	"context"
	"testing"
	"time"

	"github.com/atpost/chat-shared/callauth"
	"github.com/google/uuid"
)

// Audit M6 against the real store: an answered direct call is listed for
// signaling refresh while both sides are in it, its key is re-armed, and
// once the call ends it is neither listed nor revived.
func TestSignalingRefreshPGActiveCallOnly(t *testing.T) {
	svc, callStore, _ := lifecycleService(t)
	ctx := context.Background()
	caller, callee := uuid.New(), uuid.New()

	created, err := svc.CreateCall(ctx, caller, directCallReq(callee))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.JoinCall(ctx, caller, created.ID); err != nil {
		t.Fatal(err)
	}
	listed := func() bool {
		pairs, err := callStore.ListActiveSignalingPairs(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range pairs {
			if p.CallID == created.ID && p.InitiatorID == caller && p.ParticipantID == callee {
				return true
			}
		}
		return false
	}
	if listed() {
		t.Fatal("a ringing call was listed for refresh")
	}

	invites, _ := svc.ListPendingInvites(ctx, callee)
	if len(invites) != 1 {
		t.Fatalf("pending invites: %+v", invites)
	}
	if err := svc.AcceptInvite(ctx, callee, created.ID, invites[0].InviteID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.JoinCall(ctx, callee, created.ID); err != nil {
		t.Fatal(err)
	}
	if !listed() {
		t.Fatal("the active call was not listed for refresh")
	}

	w := NewSignalingAuthRefresher(callStore, svc.rdb, nil, time.Minute)
	if n := w.sweep(ctx); n < 1 {
		t.Fatalf("sweep extended %d keys, want the active pair", n)
	}
	if ttl := svc.rdb.TTL(ctx, callauth.PairKey(caller, callee)).Val(); ttl < callauth.DefaultTTL-time.Minute {
		t.Fatalf("pair TTL after refresh = %v", ttl)
	}

	if err := svc.LeaveCall(ctx, callee, created.ID); err != nil {
		t.Fatal(err)
	}
	if listed() {
		t.Fatal("an ended call is still listed for refresh")
	}
	w.sweep(ctx)
	if n := svc.rdb.Exists(ctx, callauth.PairKey(caller, callee)).Val(); n != 0 {
		t.Fatal("refresh revived the authorization of an ended call")
	}
}

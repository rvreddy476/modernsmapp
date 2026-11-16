package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/atpost/community-service/internal/store"
	"github.com/google/uuid"
)

/*
Guards for platform suspension (suspension.go). The gate rows live in
TestDecideCommunityAccess; these hold the body validation, the writes the
gate admits in read mode, and the wiring that announces the change.
*/

func TestNormaliseSuspension(t *testing.T) {
	long := strings.Repeat("x", suspensionNoteMaxRunes+1)
	cases := []struct {
		name               string
		suspend            bool
		code, note         string
		wantCode, wantNote string
		wantErr            bool
	}{
		{"suspend with code and note", true, " Spam ", " bot ring ", "spam", "bot ring", false},
		{"suspend without a note", true, "harassment", "", "harassment", "", false},
		{"suspend without a code", true, "", "why", "", "", true},
		{"suspend with an unknown code", true, "because", "why", "", "", true},
		{"suspend with a note too long", true, "spam", long, "", "", true},
		{"unsuspend with a note", false, "", " appeal granted ", "", "appeal granted", false},
		{"unsuspend ignores a code", false, "spam", "appeal", "", "appeal", false},
		{"unsuspend without a note", false, "", "  ", "", "", true},
		{"unsuspend with a note too long", false, "", long, "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, note, err := normaliseSuspension(tc.suspend, tc.code, tc.note)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v, wantErr=%v", err, tc.wantErr)
			}
			if err != nil {
				if !strings.HasPrefix(err.Error(), "invalid") {
					t.Fatalf("err %q must start with invalid (422 at the handler)", err)
				}
				return
			}
			if code != tc.wantCode || note != tc.wantNote {
				t.Fatalf("got (%q, %q), want (%q, %q)", code, note, tc.wantCode, tc.wantNote)
			}
		})
	}
	for _, c := range SuspensionReasonCodes {
		if c != strings.ToLower(strings.TrimSpace(c)) || !isSuspensionReasonCode(c) {
			t.Errorf("reason code %q is not normalised", c)
		}
	}
}

func TestRefuseWhileSuspended(t *testing.T) {
	c := community("public", uuid.New())
	if err := refuseWhileSuspended(c); err != nil {
		t.Fatalf("active: %v", err)
	}
	c.Status = store.CommunityStatusSuspended
	if err := refuseWhileSuspended(c); !errors.Is(err, ErrCommunitySuspended) {
		t.Fatalf("suspended: %v, want ErrCommunitySuspended", err)
	}
	if err := refuseWhileSuspended(nil); err != nil {
		t.Fatalf("nil: %v", err)
	}
}

// The writes the gate admits in read mode (an author deleting their post,
// accepting an answer, spark, stash) refuse a suspended community themselves,
// before the store write. A view is a read and is still recorded.
func TestWritesAdmittedInReadModeRefuseASuspendedCommunity(t *testing.T) {
	bodies, _ := serviceFuncs(t)
	for name, store := range map[string]string{
		"DeleteCommunityPost": "s.store.DeleteCommunityPost(",
		"AcceptAnswer":        "s.store.AcceptAnswer(",
	} {
		b := bodies[name]
		if i, j := strings.Index(b, "refuseWhileSuspended(acc.c)"), strings.Index(b, store); i < 0 || j < 0 || i > j {
			t.Errorf("%s writes before refusing a suspended community", name)
		}
	}
	gate := bodies["engagementGate"]
	if !strings.Contains(gate, "if write {") || !strings.Contains(gate, "refuseWhileSuspended(acc.c)") {
		t.Error("engagementGate no longer refuses a write on a suspended community")
	}
	for name, write := range map[string]string{
		"SparkCommunityPost":      "viewerID, true)",
		"StashCommunityPost":      "viewerID, true)",
		"RecordCommunityPostView": "viewerID, false)",
	} {
		if !strings.Contains(bodies[name], "s.engagementGate(ctx, communityID, postID, "+write) {
			t.Errorf("%s calls engagementGate without %q", name, write)
		}
	}
	// Joining refuses any non-active community, suspended included.
	if !strings.Contains(bodies["JoinCommunity"], `if c.Status != "active" {`) {
		t.Error("JoinCommunity no longer refuses a non-active community")
	}
}

// Suspend and unsuspend validate, go through the store's audited
// transaction, and announce the committed row on community.updated.
func TestSuspensionIsValidatedAuditedAndAnnounced(t *testing.T) {
	bodies, _ := serviceFuncs(t)
	set := bodies["setSuspended"]
	order := []string{
		"if actorID == uuid.Nil {",
		"normaliseSuspension(suspend, reasonCode, note)",
		"s.store.AdminSetCommunitySuspended(ctx, actorID, communityID, suspend, reasonCode, note)",
		"s.announceSuspension(ctx, out, actorID)",
	}
	last := -1
	for _, must := range order {
		i := strings.Index(set, must)
		if i < 0 || i < last {
			t.Fatalf("setSuspended lost or reordered %q", must)
		}
		last = i
	}
	if !strings.Contains(bodies["announceSuspension"], "s.producer.PublishCommunityUpdated(ctx, out.Community, actorID)") {
		t.Fatal("announceSuspension does not send the committed row on community.updated")
	}
	if !strings.Contains(bodies["SuspendCommunity"], "s.setSuspended(ctx, actorID, communityID, true,") ||
		!strings.Contains(bodies["UnsuspendCommunity"], "s.setSuspended(ctx, actorID, communityID, false,") {
		t.Fatal("SuspendCommunity / UnsuspendCommunity point the wrong way")
	}
	// The card a member sees carries the suspension.
	if !strings.Contains(bodies["GetCommunity"], "s.store.GetCommunitySuspension(ctx, communityID)") {
		t.Fatal("GetCommunity no longer shows a suspended community's members why it is read-only")
	}
}

package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/chat-message-service/internal/store/postgres"
)

// The "you joined" note (founder, 22 Oct 2026): my_join is present only for
// a group member whose history was cut at their own join.

func joinRow(method string, addedBy *uuid.UUID) *postgres.MemberJoin {
	at := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	cut := at
	return &postgres.MemberJoin{JoinedAt: at, ClearedBefore: &cut, JoinMethod: &method, AddedBy: addedBy}
}

func TestMyJoinForEachWayIn(t *testing.T) {
	viewer, owner, adder := uuid.New(), uuid.New(), uuid.New()
	cases := []struct {
		method  string
		addedBy *uuid.UUID
		wantBy  *uuid.UUID
	}{
		{postgres.JoinMethodInviteLink, nil, nil},
		// A link never names an adder, even if one were stored.
		{postgres.JoinMethodInviteLink, &adder, nil},
		{postgres.JoinMethodAdded, &adder, &adder},
		{postgres.JoinMethodAdded, nil, nil},
		{postgres.JoinMethodInvitation, &adder, &adder},
	}
	for _, c := range cases {
		row := joinRow(c.method, c.addedBy)
		got := myJoinFor(viewer, "group", &owner, row)
		if got == nil {
			t.Fatalf("%s: want a note, got none", c.method)
		}
		if got.Method != c.method || !got.JoinedAt.Equal(row.JoinedAt) {
			t.Fatalf("%s: got %+v", c.method, got)
		}
		if (got.AddedByUserID == nil) != (c.wantBy == nil) || (c.wantBy != nil && *got.AddedByUserID != *c.wantBy) {
			t.Fatalf("%s: added_by = %v, want %v", c.method, got.AddedByUserID, c.wantBy)
		}
	}
}

func TestMyJoinForNoNote(t *testing.T) {
	viewer, owner := uuid.New(), uuid.New()
	full := &postgres.MemberJoin{JoinedAt: time.Now()} // no cutoff
	added := joinRow(postgres.JoinMethodAdded, &owner)

	// Cleared chat after joining: the cutoff is not the join.
	cleared := joinRow(postgres.JoinMethodAdded, &owner)
	later := cleared.JoinedAt.Add(time.Hour)
	cleared.ClearedBefore = &later

	// A recorded way in but no cutoff (full history): still no note.
	uncut := joinRow(postgres.JoinMethodAdded, &owner)
	uncut.ClearedBefore = nil

	legacy := joinRow(postgres.JoinMethodAdded, nil)
	legacy.JoinMethod = nil // a row from before the column existed

	cases := map[string]struct {
		convType  string
		createdBy *uuid.UUID
		row       *postgres.MemberJoin
	}{
		"full history (no cutoff)": {"group", &owner, full},
		"method but no cutoff":     {"group", &owner, uncut},
		"the creator":              {"group", &viewer, added},
		"a direct chat":            {"direct", &owner, added},
		"no membership row":        {"group", &owner, nil},
		"cutoff from clear chat":   {"group", &owner, cleared},
		"pre-migration row":        {"group", &owner, legacy},
		"created method":           {"group", &owner, joinRow(postgres.JoinMethodCreated, nil)},
		"unknown method":           {"group", &owner, joinRow("teleported", nil)},
	}
	for name, c := range cases {
		if got := myJoinFor(viewer, c.convType, c.createdBy, c.row); got != nil {
			t.Fatalf("%s: want no note, got %+v", name, got)
		}
	}
}

// Every way into a group records the right method and actor, and the
// conversation read hands it back as my_join.
func TestJoinPathsRecordTheirOrigin(t *testing.T) {
	ctx := context.Background()

	t.Run("invite link", func(t *testing.T) {
		f := newGovernanceFake()
		owner, joiner, convID := uuid.New(), uuid.New(), uuid.New()
		f.addGroup(convID, owner, nil)
		f.conversations[convID].CreatedBy = &owner
		svc := newGovernanceServiceWithGraph(f, stubGraph(t, nil))
		link, err := svc.CreateInviteLink(ctx, owner, convID, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		conv, err := svc.JoinByInvite(ctx, joiner, link.Code)
		if err != nil {
			t.Fatal(err)
		}
		if conv.MyJoin == nil || conv.MyJoin.Method != "invite_link" || conv.MyJoin.AddedByUserID != nil {
			t.Fatalf("my_join after a link join: %+v", conv.MyJoin)
		}
		// The owner (creator) reading the same group gets no note.
		ownerView, err := svc.GetConversation(ctx, owner, convID)
		if err != nil {
			t.Fatal(err)
		}
		if ownerView.MyJoin != nil {
			t.Fatalf("creator must get no note, got %+v", ownerView.MyJoin)
		}
	})

	t.Run("admin add", func(t *testing.T) {
		f := newGovernanceFake()
		owner, target, convID := uuid.New(), uuid.New(), uuid.New()
		f.addGroup(convID, owner, nil)
		svc := newGovernanceServiceWithGraph(f, stubGraph(t, nil))
		out, err := svc.AddMemberWithPolicy(ctx, owner, convID, target)
		if err != nil || out.Outcome != "added" {
			t.Fatalf("add: %v / %v", out, err)
		}
		got := f.joins[intentKey(convID, target)]
		if got.Method != "added" || got.AddedBy == nil || *got.AddedBy != owner {
			t.Fatalf("admin add origin: %+v", got)
		}
		conv, err := svc.GetConversation(ctx, target, convID)
		if err != nil {
			t.Fatal(err)
		}
		if conv.MyJoin == nil || conv.MyJoin.Method != "added" || conv.MyJoin.AddedByUserID == nil || *conv.MyJoin.AddedByUserID != owner {
			t.Fatalf("my_join after an admin add: %+v", conv.MyJoin)
		}
	})

	t.Run("accepted invitation", func(t *testing.T) {
		f := newGovernanceFake()
		owner, invitee, convID := uuid.New(), uuid.New(), uuid.New()
		f.addGroup(convID, owner, nil)
		svc := newGovernanceServiceWithGraph(f, stubGraph(t, nil))
		inv, _, _ := f.CreateGroupInvitation(ctx, convID, owner, invitee)
		if err := svc.AcceptGroupInvitation(ctx, invitee, inv.ID); err != nil {
			t.Fatal(err)
		}
		got := f.joins[intentKey(convID, invitee)]
		if got.Method != "invitation" || got.AddedBy == nil || *got.AddedBy != owner {
			t.Fatalf("invitation origin: %+v", got)
		}
	})

	t.Run("group-service mirror", func(t *testing.T) {
		f := newGovernanceFake()
		owner, member, convID := uuid.New(), uuid.New(), uuid.New()
		f.addGroup(convID, owner, nil)
		svc := newGovernanceService(f)
		if err := svc.ManagedAddGroupMember(ctx, convID, member); err != nil {
			t.Fatal(err)
		}
		got := f.joins[intentKey(convID, member)]
		if got.Method != "added" || got.AddedBy != nil {
			t.Fatalf("managed add origin: %+v", got)
		}
	})

	t.Run("existing member with full history", func(t *testing.T) {
		f := newGovernanceFake()
		owner, old, convID := uuid.New(), uuid.New(), uuid.New()
		f.addGroup(convID, owner, map[uuid.UUID]string{old: "member"})
		svc := newGovernanceService(f)
		conv, err := svc.GetConversation(ctx, old, convID)
		if err != nil {
			t.Fatal(err)
		}
		if conv.MyJoin != nil {
			t.Fatalf("full-history member must get no note, got %+v", conv.MyJoin)
		}
		raw, _ := json.Marshal(conv)
		if strings.Contains(string(raw), "my_join") {
			t.Fatalf("my_join must be omitted from the wire when absent: %s", raw)
		}
	})
}

func TestMyJoinWireShape(t *testing.T) {
	by := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	raw, err := json.Marshal(&MyJoin{
		JoinedAt:      time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC),
		Method:        "added",
		AddedByUserID: &by,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"joined_at":"2026-10-22T09:00:00Z","method":"added","added_by_user_id":"11111111-1111-1111-1111-111111111111"}`
	if string(raw) != want {
		t.Fatalf("wire shape:\n got %s\nwant %s", raw, want)
	}
}

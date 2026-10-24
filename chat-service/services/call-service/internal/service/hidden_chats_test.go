package service

import (
	"context"
	"testing"

	"github.com/atpost/chat-call-service/internal/domain"
	"github.com/google/uuid"
)

// Hidden chats: which conversation a call is checked against.

type recordHidden struct {
	hidden bool
	calls  []struct {
		user, other uuid.UUID
		source      *uuid.UUID
	}
}

func (r *recordHidden) HiddenFor(_ context.Context, user, other uuid.UUID, source *uuid.UUID) bool {
	r.calls = append(r.calls, struct {
		user, other uuid.UUID
		source      *uuid.UUID
	}{user, other, source})
	return r.hidden
}

func TestCallHiddenForDirectCallsChecksSourceAndPairDM(t *testing.T) {
	rec := &recordHidden{hidden: true}
	s := (&Service{}).WithCallHiddenChecker(rec)
	callee, caller, src := uuid.New(), uuid.New(), uuid.New()
	if !s.callHiddenFor(context.Background(), callee, caller, domain.CallTypeDirectAudio, domain.SourceTypeChat, &src) {
		t.Fatal("a direct call from a hidden chat must be flagged")
	}
	c := rec.calls[0]
	if c.user != callee || c.other != caller || c.source == nil || *c.source != src {
		t.Fatalf("direct call checked against %+v", c)
	}
	// A profile-sourced direct call still checks the pair's DM (other set,
	// no source): naming another source cannot bypass a hidden DM.
	_ = s.callHiddenFor(context.Background(), callee, caller, domain.CallTypeDirectVideo, domain.SourceTypeProfile, &src)
	if c := rec.calls[1]; c.other != caller || c.source != nil {
		t.Fatalf("profile call checked against %+v", c)
	}
}

func TestCallHiddenForGroupCallsChecksOnlyTheSourceChat(t *testing.T) {
	rec := &recordHidden{hidden: true}
	s := (&Service{}).WithCallHiddenChecker(rec)
	callee, caller, src := uuid.New(), uuid.New(), uuid.New()
	if s.callHiddenFor(context.Background(), callee, caller, domain.CallTypeGroupAudio, domain.SourceTypeGroup, &src) {
		t.Fatal("a group call with no chat source can never be hidden (a hidden DM with one participant must not hide it)")
	}
	if len(rec.calls) != 0 {
		t.Fatal("group call without a chat source asked the checker")
	}
	_ = s.callHiddenFor(context.Background(), callee, caller, domain.CallTypeGroupVideo, domain.SourceTypeChat, &src)
	if c := rec.calls[0]; c.other != uuid.Nil || c.source == nil || *c.source != src {
		t.Fatalf("group call from a chat checked against %+v", c)
	}
}

func TestHistoryHiddenUsesTheOtherParticipant(t *testing.T) {
	rec := &recordHidden{hidden: true}
	s := (&Service{}).WithCallHiddenChecker(rec)
	viewer, peer := uuid.New(), uuid.New()
	// Outgoing call: the other side is the non-viewer participant.
	if !s.historyHidden(context.Background(), viewer, viewer, domain.CallTypeDirectAudio, domain.SourceTypeProfile, nil,
		[]domain.CallParticipant{{UserID: viewer}, {UserID: peer}}) {
		t.Fatal("outgoing call to a hidden DM peer must leave history")
	}
	if rec.calls[0].other != peer {
		t.Fatalf("checked against %v, want the peer", rec.calls[0].other)
	}
	// Without a checker nothing is hidden.
	if (&Service{}).historyHidden(context.Background(), viewer, peer, domain.CallTypeDirectAudio, "", nil, nil) {
		t.Fatal("no checker must hide nothing")
	}
}

package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// 10 Nov 2026: the match_closed frame an open Pulse thread receives when its
// match ends.

type matchClosedMembersStub struct {
	*idempotencyConvStoreStub
	members []postgres.Member
}

func (s matchClosedMembersStub) GetMembers(context.Context, uuid.UUID) ([]postgres.Member, error) {
	return s.members, nil
}

type matchClosedFrameWire struct {
	Type    string `json:"type"`
	Payload struct {
		ConversationID string  `json:"conversation_id"`
		MatchID        *string `json:"match_id"`
		ClosedAt       string  `json:"closed_at"`
		Reason         *string `json:"reason"`
		ClosedBy       *string `json:"closed_by"`
	} `json:"payload"`
}

func TestPublishDatingMatchClosed_ReachesBothMembersAndTheRoom(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	a, b, hider := uuid.New(), uuid.New(), uuid.New()
	conv, match := uuid.New(), uuid.New()
	s := &Service{log: slog.Default(), rdb: rdb, convStore: matchClosedMembersStub{
		idempotencyConvStoreStub: &idempotencyConvStoreStub{},
		members:                  []postgres.Member{{UserID: a}, {UserID: b}, {UserID: hider, Locked: true}},
	}}

	ctx := context.Background()
	sub := rdb.Subscribe(ctx, "chat:"+a.String(), "chat:"+b.String(), "chat:"+hider.String(), "convroom:"+conv.String())
	t.Cleanup(func() { _ = sub.Close() })
	if _, err := sub.Receive(ctx); err != nil {
		t.Fatal(err)
	}
	ch := sub.Channel()

	at := time.Date(2026, 11, 10, 12, 30, 0, 0, time.FixedZone("IST", 19800))
	s.PublishDatingMatchClosed(ctx, conv, match, at)

	got := map[string]matchClosedFrameWire{}
	deadline := time.After(3 * time.Second)
	for len(got) < 3 {
		select {
		case m := <-ch:
			var f matchClosedFrameWire
			if err := json.Unmarshal([]byte(m.Payload), &f); err != nil {
				t.Fatalf("%s: %v", m.Channel, err)
			}
			got[m.Channel] = f
		case <-deadline:
			t.Fatalf("frames received on %v, want both members and the room", keys(got))
		}
	}
	select {
	case m := <-ch:
		t.Fatalf("extra frame on %s (a member who hid the chat gets none on their personal channel)", m.Channel)
	case <-time.After(200 * time.Millisecond):
	}
	for _, chName := range []string{"chat:" + a.String(), "chat:" + b.String(), "convroom:" + conv.String()} {
		f, ok := got[chName]
		if !ok {
			t.Fatalf("no frame on %s", chName)
		}
		if f.Type != "match_closed" {
			t.Fatalf("%s: type %q", chName, f.Type)
		}
		if f.Payload.ConversationID != conv.String() || f.Payload.MatchID == nil || *f.Payload.MatchID != match.String() {
			t.Fatalf("%s: payload %+v", chName, f.Payload)
		}
		if f.Payload.ClosedAt != "2026-11-10T07:00:00Z" {
			t.Fatalf("%s: closed_at %q, want UTC", chName, f.Payload.ClosedAt)
		}
		// The other person must not learn whether they were blocked.
		if f.Payload.Reason != nil || f.Payload.ClosedBy != nil {
			t.Fatalf("%s: frame says why or who: %+v", chName, f.Payload)
		}
	}
}

func TestMatchClosedFrame_OmitsAMissingMatchID(t *testing.T) {
	body := matchClosedFrame(uuid.New(), uuid.Nil, time.Now())
	if _, ok := body["match_id"]; ok {
		t.Fatalf("frame carries match_id for a conversation without one: %v", body)
	}
	if len(body) != 2 {
		t.Fatalf("frame body = %v, want conversation_id and closed_at only", body)
	}
}

func keys(m map[string]matchClosedFrameWire) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

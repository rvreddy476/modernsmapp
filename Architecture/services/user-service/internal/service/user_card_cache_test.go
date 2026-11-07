package service

import (
	"encoding/json"
	"testing"

	"github.com/atpost/user-service/internal/store"
	"github.com/google/uuid"
)

// US-M1: store.User.Unavailable is never serialized, so the Redis card must
// carry it beside the user. A cached card that dropped it would serve a
// deactivated account for the five minutes the entry lives.
func TestUserCardCacheKeepsUnavailable(t *testing.T) {
	for _, unavailable := range []bool{true, false} {
		u := &store.User{ID: uuid.New(), DisplayName: "x", Unavailable: unavailable}
		raw, err := encodeCard(u)
		if err != nil {
			t.Fatal(err)
		}
		got, ok := decodeCard(string(raw))
		if !ok {
			t.Fatalf("a card this service wrote did not decode: %s", raw)
		}
		if got.Unavailable != unavailable || got.ID != u.ID {
			t.Fatalf("round trip: got (%v, %s), want (%v, %s)", got.Unavailable, got.ID, unavailable, u.ID)
		}
	}
}

// A card in the pre-envelope format (a bare user) cannot say whether the
// account is available, so it must read as a miss and go to Postgres.
func TestUserCardCacheOldFormatIsAMiss(t *testing.T) {
	raw, err := json.Marshal(&store.User{ID: uuid.New(), DisplayName: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := decodeCard(string(raw)); ok {
		t.Fatal("a bare cached user was trusted; a deactivated account would be served from it")
	}
	if _, ok := decodeCard("not json"); ok {
		t.Fatal("garbage decoded as a card")
	}
}

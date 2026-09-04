package service

import (
	"errors"

	"github.com/google/uuid"
)

// Tagged people on a post (reel composer, 2026-09-04).
//
// This is a server-side invariant, like the rest of create_guards.go: the
// handler parses strings into UUIDs, but what may be STORED is decided here so
// every creation path — the composer, a published draft, a scheduled post —
// agrees on the same rule.

// MaxTaggedUsers is the ceiling on distinct people one post may tag.
//
// Twenty, matching the hashtag and tag caps: a real tag list is a handful of
// friends, and anything larger is either spam or an accident, and it is also
// the fan-out bound for the notification pass that follows this one.
const MaxTaggedUsers = 20

// ErrTooManyTaggedUsers is more than MaxTaggedUsers ids in one request.
var ErrTooManyTaggedUsers = errors.New("too many tagged users")

// NormalizeTaggedUsers returns the ids to store, in the order first given.
//
// Duplicates collapse because tagging someone twice is one tag, not two. The
// author is dropped rather than rejected: a composer that pre-fills "you" in
// its people picker should not fail the whole publish over it, and a self-tag
// carries no information — the author is already on the post. The nil id is
// dropped for the same reason: it is not a person.
//
// The cap is applied to the RAW list, before dedupe. A client that sends the
// same id forty times has still sent forty ids, and "how many did you send"
// is the simplest rule to state and to test on both sides.
func NormalizeTaggedUsers(authorID uuid.UUID, ids []uuid.UUID) ([]uuid.UUID, error) {
	if len(ids) > MaxTaggedUsers {
		return nil, ErrTooManyTaggedUsers
	}
	if len(ids) == 0 {
		return []uuid.UUID{}, nil
	}
	seen := make(map[uuid.UUID]struct{}, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if id == uuid.Nil || id == authorID {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}

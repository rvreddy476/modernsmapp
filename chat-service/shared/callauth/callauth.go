// Package callauth keeps the small piece of state ws-gateway needs to
// authorize call-signaling messages without making an HTTP round trip
// to call-service for every ice_candidate.
//
// Threat model: per the realtime audit C1+C3, any authed client could
// send `call_offer` / `ice_candidate` with an arbitrary
// `target_user_id` and ws-gateway would forward it. That lets a
// malicious client ring strangers and leak the callee's ICE
// candidates (private IPs) before the callee has consented.
//
// Fix shape: call-service is the source of truth for "do these two
// users share an active or ringing call right now?". It writes the
// authorization tuple into Redis under a pair-keyed value when the
// call is created, updates it when the callee accepts (state moves
// from `ringing` → `active`), and deletes it when the call ends.
// ws-gateway reads that key on every direct-signaling message and
// drops anything that isn't covered.
//
// Key shape `call:auth:<lo>:<hi>` (alphabetically sorted user IDs)
// so writers and readers don't need to agree on direction. TTL
// bounds memory if the writer crashes without cleanup; 1 hour is
// longer than any 1:1 call.

package callauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	// DefaultTTL is the safety net: even if the writer crashes without
	// calling Clear, the key disappears after this and stale calls
	// can't authorize signaling forever.
	DefaultTTL = 1 * time.Hour

	// StateRinging covers call_offer + call_answer + call_decline +
	// call_busy + call_ring + call_accept + call_reject + call_end.
	StateRinging = "ringing"

	// StateActive additionally permits `ice_candidate` — see
	// IsAllowedFor for the policy.
	StateActive = "active"
)

// State is the auth payload Redis holds for a pair.
type State struct {
	CallID    string    `json:"call_id"`
	State     string    `json:"state"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PairKey returns a deterministic key regardless of argument order.
// Required because ws-gateway sees (sender, target) but call-service
// often sees (initiator, invitee) — same pair, different order.
func PairKey(a, b uuid.UUID) string {
	aStr, bStr := a.String(), b.String()
	if aStr > bStr {
		aStr, bStr = bStr, aStr
	}
	return fmt.Sprintf("call:auth:%s:%s", aStr, bStr)
}

// Set writes the auth tuple for the pair. Idempotent — a re-Set
// from accept after the initial Set from create just bumps state to
// active.
func Set(ctx context.Context, rdb *redis.Client, a, b uuid.UUID, callID uuid.UUID, state string) error {
	if rdb == nil {
		return errors.New("callauth: nil redis client")
	}
	if state != StateRinging && state != StateActive {
		return fmt.Errorf("callauth: invalid state %q", state)
	}
	payload, err := json.Marshal(State{
		CallID:    callID.String(),
		State:     state,
		UpdatedAt: time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	return rdb.Set(ctx, PairKey(a, b), payload, DefaultTTL).Err()
}

// Clear removes the auth tuple. Call-service does this on CallEnded
// / CallDeclined / CallTimeout — after which signaling messages
// between the pair stop authorizing.
func Clear(ctx context.Context, rdb *redis.Client, a, b uuid.UUID) error {
	if rdb == nil {
		return errors.New("callauth: nil redis client")
	}
	return rdb.Del(ctx, PairKey(a, b)).Err()
}

// Get reads the auth tuple. Returns (nil, nil) when no active call
// covers the pair — that's the "drop the signaling" signal.
func Get(ctx context.Context, rdb *redis.Client, a, b uuid.UUID) (*State, error) {
	if rdb == nil {
		return nil, errors.New("callauth: nil redis client")
	}
	raw, err := rdb.Get(ctx, PairKey(a, b)).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// IsAllowedFor encodes the signaling-type → required-state policy.
// Returns true when (state, msgType) combination is permitted to be
// relayed.
//
//   - All call_* control messages require at least `ringing`.
//   - `ice_candidate` additionally requires `active` because ICE
//     leaks the receiver's network topology to the caller; we hold
//     it back until the callee has explicitly accepted (which is
//     when call-service moves state ringing → active).
//
// Callers pass an empty state for "no Redis tuple found", which is
// always disallowed.
func IsAllowedFor(state, msgType string) bool {
	if state == "" {
		return false
	}
	switch msgType {
	case "ice_candidate":
		return state == StateActive
	case "call_offer", "call_answer", "call_ring", "call_accept",
		"call_reject", "call_decline", "call_busy", "call_end":
		return state == StateRinging || state == StateActive
	}
	// Unknown direct-signaling type: refuse by default. New types
	// must be added to this allow-list explicitly.
	return false
}

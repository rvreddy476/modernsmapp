package events

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Members-only posts (post-service posts.tier_required_id, migration 016).
//
// RULE: a post gated to a creator tier never appears in search results to
// anyone, and its text is never stored in the index. Search has no notion
// of "this viewer is a member" and must not grow one: a result row would
// disclose the text to every non-member, and a snippet or a hashtag
// aggregation over posts_v1 would leak it even without a result row. So a
// gated post is simply NOT ELIGIBLE, exactly like a private or deleted
// post: it is removed (tombstoned at its revision) when the gate is set,
// and indexed again normally when an eligibility event arrives with the
// gate cleared at a newer revision.
//
// The field is read with a local decode of the raw event payload rather
// than from shared/events' payload structs, so this consumer does not
// depend on the producer's struct change landing first. post-service puts
// it on PostCreated and PostSearchEligibilityChanged as
// `tier_required_id` (a UUID string, omitted when the post is not gated).

// membersOnlyPayload is the subset of either post payload this file reads.
// RawMessage, not string, so an unexpected JSON type is seen rather than
// failing the whole decode.
type membersOnlyPayload struct {
	TierRequiredID json.RawMessage `json:"tier_required_id"`
}

// payloadIsMembersOnly reports whether a post event says the post is gated.
//
// Absent, null and "" (after trimming) mean not gated. A non-empty string
// means gated. Any other JSON value (a number, an object, …) is a shape no
// producer is meant to send: it is treated as GATED, failing closed, rather
// than as "not gated" (which would index a members-only post) or as an
// error (which would hold the partition and dead-letter a removal).
func payloadIsMembersOnly(raw json.RawMessage) (bool, error) {
	var p membersOnlyPayload
	if err := unmarshalPayload(raw, &p); err != nil {
		return false, err
	}
	return tierValueIsGated(p.TierRequiredID), nil
}

func tierValueIsGated(v json.RawMessage) bool {
	t := bytes.TrimSpace(v)
	if len(t) == 0 {
		return false
	}
	var s string
	// JSON null decodes into a string without error and leaves it "".
	if err := json.Unmarshal(t, &s); err == nil {
		return strings.TrimSpace(s) != ""
	}
	return true
}

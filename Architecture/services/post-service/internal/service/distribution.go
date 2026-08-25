package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// Distribution policy — Module 1 P0-1 (see prompt/module-01-*-codex-approval.md).
//
// A post carries an optional, typed, versioned scalar policy in
// posts.distribution (JSONB). Scalars only: explicit per-target
// destinations (groups, communities) stay normalized in crosspost_links;
// posts.visibility remains the canonical visibility column; scheduling
// lives on the draft record; app_origin remains provenance.
//
// Precedence contract (unit-tested in distribution_test.go):
//   - distribution present  → the policy is authoritative for its fields.
//   - distribution absent   → legacy behavior is preserved exactly:
//     main_feed=true (the legacy publish_to_feed column was never read by
//     feed-service, so every post fanned out; honoring it retroactively
//     would change live behavior), notify_subscribers=true.
//   - A policy field that is omitted takes the same legacy default.

// ErrUnsupportedDistribution marks a policy that is well-formed but asks
// for behavior the platform does not implement yet. Handlers map it to
// HTTP 400 UNSUPPORTED_DISTRIBUTION — never silently ignored.
var ErrUnsupportedDistribution = errors.New("unsupported distribution field")

// ErrInvalidDistribution marks a malformed policy (unknown field, wrong
// version, bad JSON). Handlers map it to HTTP 400 INVALID_DISTRIBUTION.
var ErrInvalidDistribution = errors.New("invalid distribution policy")

// ErrNotPostAuthor is returned when an actor tries to change a post they
// don't own. Handlers map it to HTTP 403.
var ErrNotPostAuthor = errors.New("forbidden: not the post author")

// distributionPolicyVersion is the only schema version currently accepted.
const distributionPolicyVersion = 1

// DistributionPolicy is the wire + storage shape of posts.distribution.
type DistributionPolicy struct {
	Version           int   `json:"version"`
	MainFeed          *bool `json:"main_feed,omitempty"`
	NotifySubscribers *bool `json:"notify_subscribers,omitempty"`
	CreateReelPreview *bool `json:"create_reel_preview,omitempty"`
}

// ResolvedDistribution is the effective policy after defaults.
type ResolvedDistribution struct {
	MainFeed          bool
	NotifySubscribers bool
}

// ParseDistributionPolicy validates a raw policy document. nil/empty raw
// returns (nil, nil) — meaning "no policy, use legacy behavior".
func ParseDistributionPolicy(raw json.RawMessage) (*DistributionPolicy, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var p DistributionPolicy
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidDistribution, err)
	}
	// Reject trailing garbage after the object.
	if dec.More() {
		return nil, fmt.Errorf("%w: trailing data", ErrInvalidDistribution)
	}
	if p.Version != distributionPolicyVersion {
		return nil, fmt.Errorf("%w: unsupported version %d (want %d)",
			ErrInvalidDistribution, p.Version, distributionPolicyVersion)
	}
	// create_reel_preview is declared in the policy schema but not yet
	// implemented (P1-1). Requesting it must fail loudly, not no-op.
	if p.CreateReelPreview != nil && *p.CreateReelPreview {
		return nil, fmt.Errorf("%w: create_reel_preview is not yet supported", ErrUnsupportedDistribution)
	}
	return &p, nil
}

// ResolveDistribution applies the precedence contract. policy may be nil.
func ResolveDistribution(policy *DistributionPolicy) ResolvedDistribution {
	return ResolveDistributionWithLegacy(policy, LegacyDistributionFields{})
}

// LegacyDistributionFields carries the pre-policy request fields an old
// client may still send. Pointers distinguish "explicitly supplied" from
// "absent" — that distinction is the whole point (Codex P1-1).
//
// Both fields participate. fixes-v2 / Codex P1-1: `share_to_postbook` was
// previously excluded because its request DTO was a non-pointer bool, so
// absent and explicit-false looked identical. The DTO is now `*bool`,
// which captures presence without changing the JSON contract, so an
// explicit opt-out on either field is honored.
type LegacyDistributionFields struct {
	// PublishToFeed is the legacy `publish_to_feed` request field.
	PublishToFeed *bool
	// ShareToPostbook is the legacy `share_to_postbook` request field.
	ShareToPostbook *bool
}

// ResolveDistributionWithLegacy is the full precedence contract.
//
//  1. A typed `distribution` policy, when present, is authoritative for
//     every field it sets.
//  2. Otherwise an EXPLICIT legacy field expresses the creator's intent
//     and is honored. Codex P1-1: silently ignoring an explicit
//     `publish_to_feed:false` broke the old-client contract — the creator
//     asked for no feed placement and got one anyway.
//  3. Otherwise the legacy default applies (main_feed=true), which
//     preserves the historical behavior for clients that never expressed
//     an opinion.
//
// Conflict precedence when both legacy fields are supplied and disagree:
// the more restrictive wins (false beats true). Opting out of a surface
// is a deliberate act; opting in is the default, so a disagreement is
// resolved in the direction that cannot surprise the creator.
func ResolveDistributionWithLegacy(policy *DistributionPolicy, legacy LegacyDistributionFields) ResolvedDistribution {
	r := ResolvedDistribution{MainFeed: true, NotifySubscribers: true}

	// Layer 2 — explicit legacy intent (only consulted when no policy).
	if policy == nil {
		if legacy.PublishToFeed != nil {
			r.MainFeed = *legacy.PublishToFeed
		}
		if legacy.ShareToPostbook != nil && !*legacy.ShareToPostbook {
			// More restrictive wins.
			r.MainFeed = false
		}
		return r
	}

	// Layer 1 — the typed policy is authoritative.
	if policy.MainFeed != nil {
		r.MainFeed = *policy.MainFeed
	}
	if policy.NotifySubscribers != nil {
		r.NotifySubscribers = *policy.NotifySubscribers
	}
	return r
}

// LegacyIntentExpressed reports whether an old client explicitly stated a
// distribution preference. When true, the resolved values must be
// stamped onto the event (and persisted) even though no typed policy
// exists — otherwise downstream consumers fall back to their own default
// and the creator's opt-out is lost in transit.
func LegacyIntentExpressed(legacy LegacyDistributionFields) bool {
	return legacy.PublishToFeed != nil || legacy.ShareToPostbook != nil
}

// PolicyFromLegacy materializes a canonical policy from explicit legacy
// intent, so the stored row and the emitted event agree with what the
// old client asked for.
func PolicyFromLegacy(legacy LegacyDistributionFields) *DistributionPolicy {
	if !LegacyIntentExpressed(legacy) {
		return nil
	}
	resolved := ResolveDistributionWithLegacy(nil, legacy)
	mf, ns := resolved.MainFeed, resolved.NotifySubscribers
	return &DistributionPolicy{
		Version:           distributionPolicyVersion,
		MainFeed:          &mf,
		NotifySubscribers: &ns,
	}
}

// MarshalPolicy renders a policy for storage. nil → nil (SQL NULL).
func MarshalPolicy(p *DistributionPolicy) (json.RawMessage, error) {
	if p == nil {
		return nil, nil
	}
	b, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidDistribution, err)
	}
	return b, nil
}

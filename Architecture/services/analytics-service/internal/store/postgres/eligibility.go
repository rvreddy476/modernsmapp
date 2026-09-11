package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Eligibility states on analytics.content_ownership (migration 011).
//
// Money is only ever measured for content that is eligible on the day
// in question. The state is projected from post-service's
// PostSearchEligibilityChanged (the declared single contract for every
// review, visibility, takedown and deletion change) with PostDeleted and
// PostRestored as belt and braces, and it is dated: the daily rollup
// emits a row for a content item only where the state is eligible or
// the effective date is after the day. Frozen days are untouched by
// construction, so what was earned while the content was public stands.
const (
	EligibilityEligible   = "eligible"
	EligibilityIneligible = "ineligible"
	EligibilityDeleted    = "deleted"
)

// ContentTypeExclusion is the quarantine record for a content item whose
// producer-supplied content_type is not a monetizable kind. The
// ownership row stores 'unknown' for it; this row keeps the raw value so
// the exclusion can be audited and, if a new kind is ever admitted,
// found again.
type ContentTypeExclusion struct {
	ContentID      uuid.UUID
	CreatorID      uuid.UUID
	RawContentType string
	Reason         string
	// Source is the event that carried the value: events.PostCreated or
	// events.PostContentTypeChanged.
	Source string
}

// ContentEligibility is one eligibility write. Rev is the producer's
// monotonic search_rev; the write lands only when Rev advances past the
// row's eligibility_rev — or equals it, when AllowEqualRev says the
// caller is the authoritative event (which is idempotent at the same
// revision, and must be able to correct a same-revision belt-and-braces
// write that landed first) — or unconditionally when IgnoreRev is set,
// which the consumer permits only for a delete with no revision.
type ContentEligibility struct {
	ContentID     uuid.UUID
	CreatorID     uuid.UUID
	State         string
	EffectiveFrom time.Time
	Rev           int64
	AllowEqualRev bool
	IgnoreRev     bool
}

// RecordContentTypeExclusion upserts the quarantine row. Re-seeing the
// same content with a different raw value keeps the latest and bumps
// last_seen_at; the first sighting is preserved.
func (s *Store) RecordContentTypeExclusion(ctx context.Context, e ContentTypeExclusion) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO analytics.content_type_exclusions
			(content_id, creator_id, raw_content_type, reason, source, first_seen_at, last_seen_at)
		VALUES ($1, $2, $3, $4, $5, NOW(), NOW())
		ON CONFLICT (content_id) DO UPDATE SET
			creator_id = EXCLUDED.creator_id,
			raw_content_type = EXCLUDED.raw_content_type,
			reason = EXCLUDED.reason,
			source = EXCLUDED.source,
			last_seen_at = NOW()`,
		e.ContentID, e.CreatorID, e.RawContentType, e.Reason, e.Source)
	return err
}

// ClearContentTypeExclusion removes the quarantine row once a
// reclassification has given the content a canonical kind. A missing
// row is not an error.
func (s *Store) ClearContentTypeExclusion(ctx context.Context, contentID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM analytics.content_type_exclusions WHERE content_id = $1`, contentID)
	return err
}

// ApplyContentEligibility writes one eligibility state onto the
// ownership row, gated on the revision as described on ContentEligibility.
// The gate is in the statement, so two consumers racing on the same
// content cannot interleave a stale write between a read and an update.
// eligibility_rev never moves backwards, even on an IgnoreRev write.
//
// applied=false means either the revision did not advance or there is
// no ownership row for this content and creator; both are the caller's
// signal to commit past the record rather than retry it.
func (s *Store) ApplyContentEligibility(ctx context.Context, e ContentEligibility) (applied bool, err error) {
	command, err := s.db.Exec(ctx, `
		UPDATE analytics.content_ownership
		SET eligibility_state = $3,
		    eligibility_effective_from = $4,
		    eligibility_rev = GREATEST(eligibility_rev, $5),
		    projected_at = NOW()
		WHERE content_id = $1 AND creator_id = $2
		  AND (
		        $7::boolean
		     OR ($6::boolean AND $5 >= eligibility_rev)
		     OR (NOT $6::boolean AND $5 > eligibility_rev)
		  )`,
		e.ContentID, e.CreatorID, e.State, e.EffectiveFrom, e.Rev, e.AllowEqualRev, e.IgnoreRev)
	if err != nil {
		return false, err
	}
	return command.RowsAffected() > 0, nil
}

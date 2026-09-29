package postgres

// Viewer eligibility (Copyright Match plan, section 6.2, prerequisite P-4).
//
// A post is viewer-eligible only when its EFFECTIVE review status is
// approved: the base moderation status is approved AND no case-specific
// restriction is active (migration 056 generates
// posts.effective_review_status from the two). Every viewer read in this
// package spells that predicate through the helpers below and nowhere
// else; viewer_eligibility_guard_test.go fails on a raw
// `review_status = 'approved'` (which would let a restricted post through)
// and on a raw `effective_review_status = 'approved'` outside this file
// (which would let the definition fork).
//
// Writers and admin views still read and write the BASE column: the
// moderation authority (moderation_authority.go), the flagged / pending
// flips (posts.go) and the admin queue (admin_content.go) name
// review_status with other literals, on purpose.

// viewerApprovedSQL is the unqualified predicate for queries on posts alone.
const viewerApprovedSQL = `effective_review_status = 'approved'`

// viewerApproved is viewerApprovedSQL qualified with a table alias ("p" →
// "p.effective_review_status = 'approved'"). An empty alias is the
// unqualified form.
func viewerApproved(alias string) string {
	if alias == "" {
		return viewerApprovedSQL
	}
	return alias + "." + viewerApprovedSQL
}

// ViewerEligibleSQL is the whole row-level predicate — live (not deleted,
// not scheduled) and effectively approved — qualified with alias. Audience,
// age and privacy are judged by the service on the scanned row.
func ViewerEligibleSQL(alias string) string {
	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}
	return prefix + "deleted_at IS NULL AND " + viewerApproved(alias) + " AND " + prefix + "publish_at IS NULL"
}

// ReviewStatusRestricted is the effective status of a post under at least
// one active restriction. It is not a base status: no writer may store it.
const ReviewStatusRestricted = "restricted"

// EffectiveReviewStatus is the viewer-facing review status of a scanned
// row: ReviewStatusRestricted while a restriction is active, else the base.
// The Go mirror of the generated column, for the service-level gates
// (direct read, feed hydration, media access, cards) that judge a Post
// value rather than run SQL. Same definition; the guard test in the
// service package keeps those gates on this method.
func (p *Post) EffectiveReviewStatus() string {
	if p == nil {
		return ""
	}
	if p.ActiveRestrictionCount > 0 {
		return ReviewStatusRestricted
	}
	return p.ReviewStatus
}

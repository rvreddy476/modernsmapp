package main

import (
	"fmt"
	"time"

	"github.com/atpost/shared/postclassify"
	"github.com/google/uuid"
)

// postRow is one row of the scan: a post plus the ownership row it does or
// does not already have, plus the video measurement used only for the
// report-only drift check.
type postRow struct {
	ID          uuid.UUID
	AuthorID    uuid.UUID
	ContentType string
	CreatedAt   time.Time

	Visibility          string
	ReviewStatus        string
	Deleted             bool
	PublishAt           time.Time // zero when the post is not scheduled
	ContentTypeExplicit bool

	// AuthorKnown is `posts.author_id IN (SELECT id FROM public.users)`,
	// resolved in the same join. It is NOT an identity check: identity lives
	// in identity_db and this tool never leaves one database.
	AuthorKnown bool

	HasOwnership     bool
	OwnerCreatorID   uuid.UUID
	OwnerContentType string

	VideoUploadStatus  string // '' when the post has no video_metadata row
	VideoFinalCategory string
}

func (p postRow) IsScheduled(now time.Time) bool {
	return !p.PublishAt.IsZero() && p.PublishAt.After(now)
}

func (p postRow) describe(want string) string {
	return fmt.Sprintf("content_id=%s creator_id=%s content_type=%s created_at=%s",
		p.ID, p.AuthorID, want, p.CreatedAt.UTC().Format(time.RFC3339Nano))
}

type policy struct {
	includeUnknownAuthors bool
	skipDeleted           bool
}

type verdict int

const (
	verdictProject          verdict = iota // no row yet — insert it
	verdictRetype                          // same creator, different content_type — the upsert corrects it
	verdictAlreadyProjected                // byte-identical row already present — writing would only churn projected_at
	verdictConflict                        // a DIFFERENT creator already owns it — never written, always reported
	verdictSkipUnknownAuthor
	verdictSkipDeleted
	verdictSkipInvalid
)

// qualify decides what should happen to one post, and what content_type the
// ownership row should carry.
//
// # WHICH POSTS QUALIFY
//
// Almost all of them. The only exclusions are an unusable row, an
// authorship conflict, and — by default — an author who does not exist.
// Everything else is deliberate:
//
// VISIBILITY is not a filter. public / followers / private / unlisted /
// trusted / close_friends / staged all qualify. An ownership row grants no
// access and reveals nothing; it is a statement about the past ("this
// account made this content, of this kind, at this time") that only makes a
// view attributable. The people who can already see a private post generate
// real views, and those views have to land on the right creator. More
// importantly visibility is MUTABLE: a post flipped from private to public
// tomorrow would need an ownership row it can never get, because nothing
// re-runs the projection. Filtering on a mutable column is how you rebuild
// the exact bug this tool exists to fix.
//
// REVIEW_STATUS is not a filter, for the same reason and one more: rejected
// and flagged posts already accumulated views before moderation caught up,
// and a creator's history should not silently lose them. The gate that
// stops a rejected post earning is downstream — settleDay() only pays
// content_type flick/long_video from an 'eligible' creator — not here.
//
// SCHEDULED posts (publish_at in the future) qualify. They will publish;
// projecting now is the only moment this tool is being run, and a row that
// arrives early costs nothing because no events exist for unpublished
// content.
//
// DRAFTS do not appear at all: post-service keeps them in `post_drafts`,
// a separate table, and only inserts into `posts` on publish.
//
// SOFT-DELETED posts qualify by default. Ownership is immutable and the
// live consumer has no delete path, so "created, projected, later deleted"
// is a state the running system reaches on its own — this only reproduces
// it. analytics.ingest_receipts.content_id REFERENCES content_ownership
// ON DELETE RESTRICT, so a row that ever carried events must keep its
// ownership anyway. Pass -skip-deleted for the cautious position.
//
// UNKNOWN AUTHORS are skipped by default, and this is the one place the
// tool refuses to be generous. content_ownership.creator_id has no foreign
// key, exactly like auth.user_roles in identityrolebackfill, and this stack
// carries hundreds of posts minted by integration tests wrongly pointed at
// the live database. Projecting them would fill
// idx_content_ownership_creator — the index anything walking
// content_ownership by creator and created_at uses; the creator fund itself
// reads content_daily_summary — with accounts that exist nowhere. -include-unknown-authors overrides it.
//
// # ORDERING
//
// The conflict check runs BEFORE any skip. A conflict is a disagreement
// about authorship, and a disagreement must never be hidden behind
// "skipped: unknown author" or "skipped: soft-deleted".
func qualify(p postRow, pol policy) (verdict, string) {
	if p.ID == uuid.Nil || p.AuthorID == uuid.Nil {
		return verdictSkipInvalid, ""
	}
	if p.CreatedAt.IsZero() {
		// The consumer treats a PostCreated with no creation time as
		// permanently invalid. created_at is NOT NULL in `posts`, so this
		// is unreachable in practice — but a zero here would silently
		// misdate the row, which is the one mistake this tool must not make.
		return verdictSkipInvalid, ""
	}

	want := ownershipContentType(p.ContentType)

	if p.HasOwnership && p.OwnerCreatorID != p.AuthorID {
		return verdictConflict, want
	}
	if !p.AuthorKnown && !pol.includeUnknownAuthors {
		return verdictSkipUnknownAuthor, want
	}
	if p.Deleted && pol.skipDeleted {
		return verdictSkipDeleted, want
	}
	if p.HasOwnership {
		if p.OwnerContentType == want {
			return verdictAlreadyProjected, want
		}
		// Same creator, stale kind: this is exactly what
		// Store.UpdateContentType does for a live PostContentTypeChanged,
		// and the upsert's DO UPDATE reaches the same end state.
		return verdictRetype, want
	}
	return verdictProject, want
}

// ownershipContentType is the content_type decision, and it is deliberately
// a copy, not a computation.
//
// # WHY VERBATIM
//
// content_type on an ownership row is not cosmetic. It picks the
// display-view bar in model.IsDisplayView (3s/25% for short form,
// 30s/50% for everything else) and it is matched as an exact string against
// monetization_rpm_rates — flick 300 paise, long_video 5000 paise per 1000
// views — and against settleDay()'s `content_type != "long_video" &&
// != "flick" -> skip`. Getting it wrong is a 16x pricing error or a silent
// non-payment.
//
// posts.content_type is already the right answer. post-service resolves it
// at create time (resolveVideoContentType, which defers to
// shared/postclassify when the author expressed no intent) and the
// MediaTranscodeConsumer rewrites the column after transcode measures the
// real duration and dimensions. So the column holds the value a live
// PostCreated + PostContentTypeChanged pair would have converged the
// projection to. Copying it is not laziness; recomputing it would make this
// tool a SECOND classifier, which is precisely what
// analytics/internal/model/view_rules.go was rewritten to eliminate
// ("The platform has exactly one content-type rule and it lives in
// shared/postclassify").
//
// # LEGACY KINDS ARE NOT REMAPPED
//
// `posts` still permits 'reel' and 'video' from before the flick/long_video
// split. They are NOT rewritten to flick/long_video here. postclassify
// already recognises both as legacy synonyms — IsShortForm("reel") and
// IsLongForm("video") are true — so the display-view bar is correct for
// them either way. What a remap WOULD change is money: monetization_rpm_rates
// has no 'reel' or 'video' row and settleDay() pays only the two canonical
// kinds, so rewriting 'reel' to 'flick' would start paying content that has
// never been priced. That is a product decision about a rate card, not a
// side effect a measurement backfill gets to smuggle in. If those rows
// should be canonicalised, post-service should do it and fan
// PostContentTypeChanged, and this projection will follow.
//
// The empty-string fallback mirrors the consumer exactly: a PostCreated
// with no content_type is projected as "post".
func ownershipContentType(postsContentType string) string {
	if postsContentType == "" {
		return "post"
	}
	return postsContentType
}

// detectDrift reports — and only reports — a post whose stored content_type
// disagrees with what its transcoded video measures, in the specific case
// where post-service's own reclassifier WOULD have changed it.
//
// It is the mirror of reclassifyDecision in
// services/post-service/internal/consumers/media.go: an explicit kind is the
// author's choice and stands, a flick is never downgraded even without the
// explicit flag, and anything else takes the measured type. Only a video
// whose upload_status is 'ready' has been measured; 'pending' and
// 'processing' rows carry a placeholder final_category and mean nothing yet.
//
// This tool does not act on it. If post-service missed a flip, the fix is in
// post-service — it will publish PostContentTypeChanged and the consumer
// will correct the row this backfill wrote. Silently writing the measured
// type here would leave `posts` and `analytics` permanently disagreeing.
//
//	mirrors: services/post-service/internal/consumers/media.go (reclassifyDecision)
func detectDrift(p postRow) bool {
	if p.VideoUploadStatus != "ready" || p.VideoFinalCategory == "" {
		return false
	}
	if p.ContentTypeExplicit || p.ContentType == postclassify.Flick {
		return false // the author's kind stands; not drift
	}
	return p.ContentType != p.VideoFinalCategory
}

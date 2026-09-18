package postgres

import (
	"context"

	"github.com/google/uuid"
)

// MediaIsOnPublicPost backs delivery.PublicPostLookup — the open-graph poster
// authority for anonymous callers (2026-09-18).
//
// WHY THIS QUERY LIVES HERE RATHER THAN IN post-service
//
// media-service deliberately does not own audiences, and asking the owning
// service is the rule everywhere else in delivery/authz.go. That rule is not
// broken here so much as bypassed: post-service's media-access contract has no
// anonymous branch at all (it 403s an unparseable viewer and denies uuid.Nil),
// and its post rule treats "unlisted" and a blank visibility as visible, which
// is wider than a search-engine crawler may ever be given. So instead of
// delegating a question the authority cannot answer, this asks a STRICTLY
// SMALLER one that has a single right answer regardless of who is asking:
// is this asset carried by a post that is published to the entire internet?
//
// `posts` and `post_media` live in the same `app` database as `media_assets`
// (see voice.go and asset_purge.go, which already read them), so this is one
// index-backed EXISTS on the read path and not a second network hop.
//
// EVERY CLAUSE IS A REFUSAL SOMEBODY WOULD OTHERWISE GET WRONG
//
//   - visibility = 'public' exactly. Not 'unlisted' (link-only, and indexing
//     it is the leak), not 'followers'/'circle'/'close_friends' (an audience
//     an anonymous caller is by definition not in), not a blank one
//     (post-service reads a blank as public for signed-in viewers; an unstated
//     audience is not a public one), and not a value added later — the
//     equality fails closed.
//   - review_status = 'approved'. A flagged, rejected, pending or
//     needs_changes post is not public.
//   - deleted_at IS NULL. A soft-deleted post keeps its rows; its poster must
//     stop resolving the moment the author deletes it.
//   - publish_at IS NULL. A scheduled post's row exists before its time and is
//     author-only until the schedule worker clears this.
//   - moderation_status passed/approved and processing_status ready on the
//     asset. The canonical media gate is not waived by the post being public:
//     a pending or rejected asset has no anonymous reading, and a half-
//     processed one has no poster worth serving.
//
// A post can reference an asset two ways — an attachment row, or as the
// post's chosen cover frame — and the poster of a Tube video is usually the
// latter, so both count.
func (s *MediaAssetStore) MediaIsOnPublicPost(ctx context.Context, mediaID string) (bool, error) {
	id, err := uuid.Parse(mediaID)
	if err != nil {
		// Not a resolvable asset id. False is a denial, which is the correct
		// reading: there is no public post behind a malformed id.
		return false, nil
	}
	var public bool
	err = s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM media_assets m
			JOIN posts p ON (
				p.cover_media_id = m.id
				OR EXISTS (
					SELECT 1 FROM post_media pm
					WHERE pm.post_id = p.id AND pm.media_id = m.id
				)
			)
			WHERE m.id = $1
			  AND m.processing_status = 'ready'
			  AND COALESCE(m.moderation_status, 'pending') IN ('passed', 'approved')
			  AND p.deleted_at IS NULL
			  AND p.publish_at IS NULL
			  AND lower(btrim(p.visibility)) = 'public'
			  AND lower(btrim(p.review_status)) = 'approved'
		)`, id).Scan(&public)
	if err != nil {
		// Reported, never swallowed into a false. The gate turns this into a
		// retryable 503 rather than teaching a crawler that the picture is gone.
		return false, err
	}
	return public, nil
}

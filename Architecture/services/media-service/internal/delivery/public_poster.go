package delivery

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Open Graph posters for shared links (2026-09-18).
//
// A link to an MTube video carries `og:image` pointing at
// `/v1/media/{mediaId}/serve/{variant}`, because that is the only URL that
// does not expire — feed variants are signed for five minutes and a crawler
// may fetch hours later. The crawler is anonymous, post media is
// protected-class, and the post content authority denies every anonymous
// viewer, so the tag resolved to a 404 and every shared video and every search
// result was pictureless.
//
// WHY NOT `allowAnonymous`
//
// `allowAnonymous` on HTTPContentAuthorizer does exactly one thing: it skips
// this service's local "no viewer, no audience decision" pre-flight so the
// request still reaches the content authority. It does NOT short-circuit the
// authority, and it does NOT decide anything itself. Two consequences:
//
//  1. It would not work. post-service's /v1/internal/media-access parses
//     viewer_id as a UUID and answers 403 for an unparseable one, and
//     ViewerMayAccessMedia denies uuid.Nil outright ("nil_id"). Setting the
//     flag on the post authorizer buys one extra HTTP round trip and the same
//     denial. (It is dead weight on the post path today for a second reason:
//     every in-process caller passes uuid.Nil.String(), never "", so the
//     pre-flight it guards never fires. profile and commerce work for
//     anonymous callers because THEY accept a nil viewer, not because of the
//     flag.)
//
//  2. Even if post-service grew an anonymous branch, the flag would delegate
//     the whole question, and post-service's own post rule admits "unlisted"
//     and an empty visibility alongside "public" (evaluatePostMediaVisibility).
//     Unlisted means "only people I send the link to" — precisely what must
//     NOT be handed to a search-engine index.
//
// So the flag is not used. What follows is a narrower, self-contained rule
// that can only ever ADD an allow on top of the existing authorities, for one
// shape of request: an anonymous caller asking for a still image of an asset
// carried by a post that is public to the entire internet.

// PublicPostLookup answers one deliberately small question.
//
// It is a positive authority: a true means "this asset hangs off a live,
// approved, unscheduled, undeleted post whose visibility is literally public,
// and the asset itself passed moderation". Everything softer than that —
// unlisted, followers, circle, private, a blank visibility, a pending or
// rejected asset, a scheduled or soft-deleted post — is false.
type PublicPostLookup interface {
	MediaIsOnPublicPost(ctx context.Context, mediaID string) (bool, error)
}

// WithPublicPoster wires the open-graph poster authority. Called from main.go
// with the media-asset store. Leaving it unwired is safe: the gate then
// behaves exactly as it did before this existed.
func (g *Gate) WithPublicPoster(lookup PublicPostLookup) *Gate {
	if g != nil {
		g.publicPoster = lookup
	}
	return g
}

// anonymousImageVariants is what an anonymous caller may read of a public
// post, and it is a closed list of derived STILLS.
//
// A crawler needs a picture, not the film. The original is excluded on
// purpose — for an image post the original is the full-resolution file the
// author uploaded, and for a video it is the source mezzanine; neither is a
// poster. Video renditions (360p…4k), the HLS master and its segments, the
// preview GIF and the audio track are excluded for the same reason: none of
// them is what an Open Graph tag asks for, and admitting them would mean an
// anonymous fetch of the whole work.
//
// These names are the ones the pipeline actually writes — see
// processing.DefaultImageVariants (thumb_150, small_480, medium_1080) and
// service.DefaultVideoRenditions (thumb_150, thumb_300).
var anonymousImageVariants = map[string]bool{
	"thumb_150":   true,
	"thumb_300":   true,
	"small_480":   true,
	"medium_1080": true,
}

// AnonymousImageVariant reports whether variant is a still an anonymous caller
// may read of a public post.
func AnonymousImageVariant(variant string) bool {
	return anonymousImageVariants[strings.ToLower(strings.TrimSpace(variant))]
}

// nilViewerID is uuid.Nil rendered. Every read path in this service resolves a
// missing X-User-Id to uuid.Nil and stringifies it, so this — not "" — is what
// an anonymous request looks like by the time it reaches the gate. Both are
// treated as anonymous so a future caller that passes the empty string cannot
// accidentally be read as an identified viewer.
const nilViewerID = "00000000-0000-0000-0000-000000000000"

// AnonymousViewer reports whether viewerID carries no identity.
func AnonymousViewer(viewerID string) bool {
	trimmed := strings.TrimSpace(viewerID)
	return trimmed == "" || trimmed == nilViewerID
}

// URLForVariant is URLFor with the requested variant name in hand, so the one
// widening this file introduces can be scoped to stills.
//
// The ordinary decision runs FIRST and unchanged. The poster path is only ever
// consulted after it has already refused, so nothing this adds can narrow an
// existing allow, and no signed-in viewer's request changes shape or cost.
//
// A refusal that the poster path cannot overturn is returned exactly as the
// ordinary path produced it: a denial stays a 404, an outage stays a
// retryable 503. A poster lookup that itself fails is unresolved rather than
// denied — a database blip must not teach a crawler that a public video has no
// picture.
func (g *Gate) URLForVariant(ctx context.Context, viewerID, mediaID, variant, objectKey string) (string, error) {
	if g == nil || g.signer == nil {
		return "", fmt.Errorf("%w: delivery gate not configured", ErrDeliveryUnresolved)
	}
	url, err := g.URLFor(ctx, viewerID, mediaID, objectKey)
	if err == nil {
		return url, nil
	}
	if g.publicPoster == nil || !AnonymousViewer(viewerID) || !AnonymousImageVariant(variant) {
		return "", err
	}
	public, lookupErr := g.publicPoster.MediaIsOnPublicPost(ctx, mediaID)
	if lookupErr != nil {
		return "", fmt.Errorf("%w: public poster lookup: %v", ErrDeliveryUnresolved, lookupErr)
	}
	if !public {
		return "", err
	}
	// Signed and bounded like every other protected byte. The poster is not
	// promoted to the public class: the URL still expires, so a leaked one
	// cannot outlive the post going private.
	return g.signer.SignProtected(objectKey, MaxProtectedTTL, time.Now())
}

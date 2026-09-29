package delivery

import (
	"context"
	"fmt"
)

// Signed-out reads of post media (2026-09-29, founder decision 2).
//
// HISTORY, AND WHY THE POSTER AUTHORITY IS GONE
//
// From 2026-09-18 this file held a second, media-service-local authority for
// anonymous callers: a SQL EXISTS over posts / post_media / media_assets that
// could ADD an allow for a still image of a public post after the post
// authority had refused every signed-out viewer. It existed because
// post-service's /v1/internal/media-access had no anonymous branch at all,
// and a shared MTube link's og:image resolved to a 404.
//
// post-service now answers the signed-out question itself (viewer_id "",
// service.anonymousMayAccessPost): exactly public, approved, live,
// unscheduled, not 18+, not members-only, by a public and unhidden account.
// That rule is the canonical one, it covers stills AND playback (public
// videos play without sign-in), and it consults facts this service cannot —
// the author's account_visibility lives in identity's database and is
// resolved through graph-service, and the 18+ flag and the hidden-authors
// table are post-service's. A local SQL that could overturn a resolved "no"
// from that rule would be exactly the second policy delivery/authz.go warns
// against: free to disagree, and wrong whenever it did (P-8 b: the old query
// checked neither account privacy, nor hidden authors, nor age_restricted).
//
// So the poster path was retired rather than patched: an anonymous still of
// a public post is delivered by the ordinary gate, and a resolved denial
// from post-service is final. A post-service outage now answers 503
// (retryable) to a crawler instead of consulting a weaker local rule;
// crawlers retry a 5xx and cache a 404, so that is the better failure too.

// URLForVariant is URLFor for a read that named a variant. Kept as the
// call site's entry point (service.deliveryURLForVariant); the variant no
// longer changes the decision, because there is no longer a stills-only
// authority behind it.
func (g *Gate) URLForVariant(ctx context.Context, viewerID, mediaID, variant, objectKey string) (string, error) {
	if g == nil || g.signer == nil {
		return "", fmt.Errorf("%w: delivery gate not configured", ErrDeliveryUnresolved)
	}
	_ = variant
	return g.URLFor(ctx, viewerID, mediaID, objectKey)
}

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/atpost/post-service/internal/store/postgres"
)

// Slice C create-post guards.
//
// These are server-side invariants, not client conveniences. Every one of them
// is reachable by a hostile or simply older client that never learned the rule,
// so none of them may live only in Android.

var (
	// ErrEmptyPost is a post with neither text nor media. C-LB-1.3.
	ErrEmptyPost = errors.New("a post needs text or an image")

	// ErrTextTooLong is text beyond the published ceiling. C-LB-1.3.
	ErrTextTooLong = errors.New("text is longer than the maximum")

	// ErrMediaNotFound / ErrMediaNotOwned / ErrMediaNotReady are the three
	// distinct authority failures. C-LB-4.
	//
	// ErrMediaNotOwned is deliberately worded so the response reveals NOTHING
	// about the foreign asset — not its owner, kind, size, or existence beyond
	// "you may not use it" (C-LB-4.2). Probing for other users' media ids must
	// not become a way to learn what they uploaded.
	ErrMediaNotFound = errors.New("media not found")
	ErrMediaNotOwned = errors.New("media cannot be attached by this user")
	ErrMediaNotReady = errors.New("media is not ready to publish")

	// ErrMediaTypeMismatch: the asset cannot back this kind of post (C-LB-4.1).
	ErrMediaTypeMismatch = errors.New("media type does not match the post")
)

// MaxPostTextRunes is the create-post ceiling in Unicode CODE POINTS.
//
// Code points, not bytes and not UTF-16 units. A 5,000-byte limit would reject
// roughly 1,600 Devanagari characters while accepting 5,000 Latin ones, which
// for an India-first product is a limit that discriminates by script. Android
// counts the same way (`codePointCount`), so the two agree on the boundary
// instead of disagreeing by language.
const MaxPostTextRunes = 5000

// ValidatePostContent enforces the non-empty and length invariants.
//
// Whitespace-only text is empty: a post containing three spaces is not content,
// and accepting it creates a row nobody can read and every surface must render.
func ValidatePostContent(text string, mediaCount int) error {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" && mediaCount == 0 {
		return ErrEmptyPost
	}
	// Counted on the RAW text, not the trimmed copy: the ceiling is about what
	// gets stored, and the stored value is what the client sent.
	if utf8.RuneCountInString(text) > MaxPostTextRunes {
		return fmt.Errorf("%w: %d code points, limit %d",
			ErrTextTooLong, utf8.RuneCountInString(text), MaxPostTextRunes)
	}
	return nil
}

// verifyMediaAuthority proves the author may publish every supplied asset.
//
// Three independent questions, each of which has its own failure mode:
//
//   - does the asset exist? A missing row used to be silently treated as an
//     image, so a typo'd or already-reclaimed id produced a post with a broken
//     attachment;
//   - did THIS user upload it? Without this, media ids — which are just UUIDs
//     in URLs — are enough to attach someone else's photo to your own post;
//   - is it finished and safe? Publishing a still-processing asset shows a
//     broken image; publishing a moderation-rejected one publishes exactly the
//     content review already refused.
//
// One batched query for all three (`BatchGetMediaOwnership`), so this costs one
// round trip regardless of attachment count.
func (s *Service) verifyMediaAuthority(
	ctx context.Context,
	authorID uuid.UUID,
	mediaIDs []uuid.UUID,
	contentType, postType string,
) error {
	if len(mediaIDs) == 0 {
		return nil
	}

	ownership, err := s.pgStore.BatchGetMediaOwnership(ctx, mediaIDs)
	if err != nil {
		// FAIL CLOSED. An unavailable authority is not permission: degrading to
		// "attach anyway" is precisely the behaviour this check replaces.
		return fmt.Errorf("verify media authority: %w", err)
	}

	for _, mediaID := range mediaIDs {
		m, ok := ownership[mediaID]
		if err := checkMediaAuthority(mediaID, authorID, m, ok, contentType, postType); err != nil {
			return err
		}
	}
	return nil
}

// checkMediaAuthority is the whole per-asset decision, as a pure function.
//
// Split out of the loop deliberately. `s.pgStore` is a concrete
// `*postgres.Store`, so nothing above this line can be exercised without a
// live database — which meant the ownership, readiness, moderation and type
// gates, the four rules that decide whether one user can publish another
// user's or an unreviewed asset, had no test at all. As a pure function over
// the row it is a table test, and NC-C4A/NC-C4B mutate it directly.
//
// [found] is passed separately rather than inferred from a zero-valued [m],
// because a zero `MediaOwnership` is indistinguishable from a real row whose
// uploader is the nil UUID.
func checkMediaAuthority(
	mediaID, authorID uuid.UUID,
	m postgres.MediaOwnership,
	found bool,
	contentType, postType string,
) error {
	if !found {
		return fmt.Errorf("%w: %s", ErrMediaNotFound, mediaID)
	}
	if m.UploaderID != authorID {
		// No id echoed and no detail about the asset — see ErrMediaNotOwned.
		return ErrMediaNotOwned
	}

	// CONFIRMED, not "ready". Product decision 2026-09-04: a reel is
	// publishable the moment its upload finishes, like Instagram/YouTube.
	// Transcoding is a background job that improves quality later and must
	// not gate publishing. So the create gate asks only "did the bytes
	// arrive and did nothing refuse them?":
	//
	//   - `pending_upload`: the bytes never arrived (no /confirm). Refused —
	//     a post that attaches it would render a broken image forever.
	//   - `failed` / `rejected`: processing gave up, or confirm refused the
	//     file (magic bytes). Refused with the same error as before.
	//   - `uploaded` / `processing` / `ready`: confirmed. Accepted.
	//
	// Still an ALLOWLIST, not a denylist: an empty or unknown status is
	// refused. `media_assets.processing_status` is CHECK-constrained, so an
	// unknown value is a bug, not a legacy population to be generous about.
	//
	// The exact `ready` + `passed` rule this replaces did not go away — it
	// became the AUTHOR-ONLY VISIBILITY rule (mediaPublishable, processing.go):
	// until every attached asset is ready and passed, the post is returned
	// only to its author. Nobody else can reach an unprocessed or unscanned
	// asset through a post.
	if !mediaConfirmed(m.ProcessingStatus) {
		return fmt.Errorf("%w: %s is %q, not confirmed",
			ErrMediaNotReady, mediaID, m.ProcessingStatus)
	}

	// Moderation: only a REFUSAL blocks creation. `pending` (not yet
	// scanned) and `manual_review` (scanner produced no verdict) are
	// accepted here and held author-only by the visibility rule until a
	// `passed` verdict lands; `rejected` is content review already refused
	// and is never attachable. Empty is refused: the column defaults to
	// `pending`, so empty means a row this service does not understand.
	if m.ModerationStatus == "" || m.ModerationStatus == mediaRejected {
		return fmt.Errorf("%w: %s moderation is %q",
			ErrMediaNotReady, mediaID, m.ModerationStatus)
	}

	// The asset's KIND must suit the post it is being attached to.
	//
	// `Kind` was read and then never used. Attaching a video to a
	// `post_type=image` post produces a post whose own reader resolves the
	// wrong renderer, and attaching an image to a video content type
	// produces a Flick with no playable source.
	if err := checkMediaCompatibility(contentType, postType, m.Kind); err != nil {
		return fmt.Errorf("%w (%s)", err, mediaID)
	}
	return nil
}

// checkMediaCompatibility rejects an asset whose kind cannot back the post.
//
// Deliberately permissive where the SHARED route legitimately serves other
// products — Slice C's Android client only ever sends `content_type=post` with
// `post_type=text|image`, but this same handler creates Flicks and long videos
// for existing callers, and tightening those is not this slice's business.
//
// What it does refuse is the mismatch: a video where the post claims an image,
// or an image where the post claims video. Both produce a post its own reader
// cannot render.
func checkMediaCompatibility(contentType, postType, kind string) error {
	switch {
	case isVideoContentType(contentType):
		// Flick / long_video are backed by video. An `audio` asset is also
		// legitimate for some existing callers, so only `image` is refused.
		if kind == mediaKindImage {
			return fmt.Errorf("%w: %s content cannot be backed by an image",
				ErrMediaTypeMismatch, contentType)
		}

	case postType == postTypeImage:
		if kind != mediaKindImage {
			return fmt.Errorf("%w: an image post cannot be backed by %s media",
				ErrMediaTypeMismatch, kind)
		}

	case postType == postTypeText:
		// A "text" post that carries media is what the composer sends before it
		// knows better, and older clients do it too. Only a video is refused:
		// silently publishing a video as a text post is how PostTube content
		// leaks into the social feed with no player.
		if kind == mediaKindVideo {
			return fmt.Errorf("%w: a text post cannot be backed by video",
				ErrMediaTypeMismatch)
		}
	}
	return nil
}

const (
	mediaReady     = "ready"
	mediaPassed    = "passed"
	mediaRejected  = "rejected"
	mediaKindImage = "image"
	mediaKindVideo = "video"
	postTypeImage  = "image"
	postTypeText   = "text"
)

// FingerprintOf hashes an already-canonical byte representation of a request.
//
// The CANONICALISATION lives with the request type (see the HTTP layer's
// `canonicalCreateRequest`), because only that layer knows the full set of
// fields the route accepts. This function is deliberately dumb: it must not
// know or care which fields exist, so it cannot go stale when one is added.
func FingerprintOf(canonical []byte) string {
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// CreateFingerprint binds an idempotency key to the payload it was minted for.
//
// DEPRECATED for the HTTP create route, which now uses the canonical whole-
// request fingerprint. Kept because it is a useful, explicit hash for internal
// callers that construct a `CreatePostInput` directly and have no wire request
// to canonicalise.
//
// It hashes only text, visibility, content type, post type, language and media
// ids — which is exactly why it was wrong for the HTTP route: an idempotency
// key is a claim about ONE request, and a fingerprint that ignores most of the
// request lets a materially different second request replay the first. See
// `canonicalCreateRequest`.
//
// Without it, a client that edits its text and retries under the same key gets
// the ORIGINAL post replayed and is told it succeeded — the edit is silently
// discarded. With it, that retry is a 409 and the client mints a new key.
//
// The inputs are the fields a user can actually see and change. Deliberately
// NOT the whole request: including server-defaulted or transport fields would
// make a byte-identical user intent hash differently across client versions and
// turn every app update into a wave of spurious conflicts.
//
// Length-prefixed rather than delimiter-joined, so text containing the
// delimiter cannot be arranged to collide with a different field split.
func CreateFingerprint(text, visibility, contentType, postType, language string, mediaIDs []uuid.UUID) string {
	h := sha256.New()
	write := func(s string) {
		fmt.Fprintf(h, "%d:%s|", len(s), s)
	}
	write(text)
	write(visibility)
	write(contentType)
	write(postType)
	write(language)
	// Order matters and is the caller's: [a,b] and [b,a] are different posts.
	for _, id := range mediaIDs {
		write(id.String())
	}
	return hex.EncodeToString(h.Sum(nil))
}

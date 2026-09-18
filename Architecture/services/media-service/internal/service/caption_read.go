package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/atpost/media-service/internal/captions"
	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Read authorization for captions (subtitle reads).
//
// THE HOLE THIS CLOSES
//
// `GET /v1/subtitles/:mediaId` and `/status` took no viewer and asked no
// authority. `/status` returns the transcript itself in `text`. So the
// full spoken content of a private video, an unlisted one, one still
// waiting on moderation, or one whose post had been taken down was
// readable by anyone who knew the media UUID — while the same asset's
// bytes were correctly refused by the delivery Gate one route over. A
// transcript is not metadata; it is the content.
//
// WHAT "THE SAME GATE" MEANS HERE
//
// A caption is a derivative of the asset, so the audience decision is the
// asset's. AuthorizeMediaRead therefore ends in delivery.Gate.AuthorizeAsset
// — the exact call GET /v1/media/:mediaId/url makes before signing — over
// the exact same key set. Two local facts are settled before that call
// because media-service, not the content authority, owns them:
//
//   - the dating scope (a dating photo is its owner's alone), identical to
//     the check every media read already makes; and
//   - moderation. An asset that has not been released is the uploader's
//     alone, whatever a post says about its audience. This is why an owner
//     can read their own pending asset's captions in the studio while a
//     stranger cannot.
//
// Everything else — private, unlisted, followers-only, blocked, deleted
// post — belongs to the service that owns the audience, and is answered by
// the content authority behind the Gate. Fail-closed is inherited: an
// unreachable authority is ErrDeliveryUnresolved (503), never a served
// transcript.

// ErrCaptionTrackNotFound means no stored track for that media/language.
var ErrCaptionTrackNotFound = errors.New("captions: no track for that language")

// captionReadVerdict is the part of the decision that can be made from the
// asset row alone.
type captionReadVerdict int

const (
	// captionReadDenied: settled locally, no authority call can change it.
	captionReadDenied captionReadVerdict = iota
	// captionReadOwner: the uploader, who may always read their own
	// captions — including while the asset is pending moderation.
	captionReadOwner
	// captionReadAskAuthority: the audience question belongs to the
	// service that owns the referencing content.
	captionReadAskAuthority
)

// moderationReleased reports whether an asset has cleared moderation.
//
// Two spellings exist in this service: images write "passed" (the
// media_assets CHECK set) and the voice pipeline writes "approved". Both
// mean released. Anything else — pending, failed, rejected, empty —
// does not.
func moderationReleased(status string) bool {
	return status == "passed" || status == "approved"
}

// captionReadLocalVerdict is the store-free half of the read gate.
func captionReadLocalVerdict(media *postgres.MediaAsset, viewerID uuid.UUID) captionReadVerdict {
	if media == nil {
		return captionReadDenied
	}
	if DatingScopeDenies(media, viewerID) {
		return captionReadDenied
	}
	if viewerID != uuid.Nil && viewerID == media.UploaderID {
		return captionReadOwner
	}
	if !moderationReleased(media.ModerationStatus) {
		return captionReadDenied
	}
	return captionReadAskAuthority
}

// AuthorizeMediaRead decides whether viewerID may read caption content for
// mediaID. uuid.Nil is an anonymous viewer, which is legitimate for public
// media and fails everything else.
//
// Errors are the delivery package's, so handlers map them with the same
// writeDeliveryError every other read uses: a resolved denial is answered
// as not-found (an asset's existence is not disclosed), an unresolved one
// as a retryable 503.
func (s *Service) AuthorizeMediaRead(ctx context.Context, viewerID, mediaID uuid.UUID) error {
	media, err := s.pgStore.GetMediaWithVariants(ctx, mediaID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Deleted or never existed — the same answer either way.
			return delivery.ErrDeliveryDenied
		}
		return fmt.Errorf("%w: load media: %v", delivery.ErrDeliveryUnresolved, err)
	}
	return authorizeCaptionRead(ctx, s.gate, media, viewerID)
}

// authorizeCaptionRead is the whole decision with the store call already
// made, so it can be exercised against a real delivery.Gate and a real
// asset row without a database.
func authorizeCaptionRead(ctx context.Context, gate *delivery.Gate, media *postgres.MediaAsset, viewerID uuid.UUID) error {
	switch captionReadLocalVerdict(media, viewerID) {
	case captionReadOwner:
		return nil
	case captionReadDenied:
		return delivery.ErrDeliveryDenied
	}
	if gate == nil {
		return fmt.Errorf("%w: delivery gate not configured", delivery.ErrDeliveryUnresolved)
	}
	return gate.AuthorizeAsset(ctx, viewerID.String(), media.ID.String(), assetDeliveryKeys(media))
}

// assetDeliveryKeys is the key set GetMediaURL authorizes over. Same asset,
// same keys, same class decision.
func assetDeliveryKeys(media *postgres.MediaAsset) map[string]string {
	keys := map[string]string{"original": media.StorageKey}
	for _, v := range media.Variants {
		keys[v.Name] = v.ObjectKey
	}
	if media.HLSMasterKey != "" {
		keys["hls"] = media.HLSMasterKey
	}
	return keys
}

// SubtitleTrackPath is the canonical URL of a rendered caption track.
//
// It is the ONLY value ever stored in media_subtitles.content_url, and the
// only thing a <track src> should point at for this service's captions.
func SubtitleTrackPath(mediaID uuid.UUID, language string) string {
	return fmt.Sprintf("/v1/subtitles/%s/track/%s.vtt", mediaID, language)
}

// CaptionTrackVTT renders the stored track for one language as WebVTT.
//
// Callers MUST have passed AuthorizeMediaRead first; this function does no
// authorization of its own, exactly like the store call it replaces.
func (s *Service) CaptionTrackVTT(ctx context.Context, mediaID uuid.UUID, language string) (string, error) {
	language = strings.TrimSpace(language)
	if !validLanguageTag(language) {
		return "", ErrCaptionTrackNotFound
	}
	subs, err := s.pgStore.GetSubtitles(ctx, mediaID)
	if err != nil {
		return "", err
	}
	var track *postgres.MediaSubtitle
	for i := range subs {
		if strings.EqualFold(subs[i].Language, language) {
			track = &subs[i]
			break
		}
	}
	if track == nil {
		return "", ErrCaptionTrackNotFound
	}

	var durationMs int64
	if media, mediaErr := s.pgStore.GetMedia(ctx, mediaID); mediaErr == nil {
		durationMs = int64(media.DurationMsValue())
	}
	body := captions.RenderWebVTT(subtitleCues(track, durationMs))
	if strings.TrimSpace(strings.TrimPrefix(body, "WEBVTT")) == "" {
		// A row with no usable text is not a track. Saying so is better
		// than handing the player an empty file it renders as "captions
		// on, nothing shown".
		return "", ErrCaptionTrackNotFound
	}
	return body, nil
}

// subtitleCues turns a stored row into cues, preferring word timings.
func subtitleCues(track *postgres.MediaSubtitle, durationMs int64) []captions.Cue {
	var words []captions.Word
	if len(track.WordLevelJSON) > 0 {
		if err := json.Unmarshal(track.WordLevelJSON, &words); err != nil {
			// Malformed timings degrade to the flat transcript rather
			// than failing the track.
			words = nil
		}
	}
	return captions.BuildCues(track.Content, words, durationMs)
}

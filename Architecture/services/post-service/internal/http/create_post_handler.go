// create_post_handler.go holds POST /v1/posts: the request and response
// shapes, the body cap, the error mappers for create guards and credits,
// and the CreatePost handler itself. Moved out of handler.go unchanged.

package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type CreatePollRequest struct {
	Question       string   `json:"question" binding:"required"`
	Options        []string `json:"options" binding:"required,min=2,max=6"`
	AllowsMultiple bool     `json:"allows_multiple"`
	DurationHours  *int     `json:"duration_hours"`
}

type CreatePostRequest struct {
	Text string `json:"text"`
	// friends = mutual follows (2026-10-18). "Only you" is private with an
	// empty share list; there is no separate value for it.
	Visibility string `json:"visibility" binding:"required,oneof=public followers friends private unlisted"`
	// VisibilityPolicy (mode / allow_lists / allow_users / deny_users) was
	// parsed here and never forwarded, so a client that sent one published
	// to `visibility` alone and was told nothing. No client sends it; the
	// field is kept only so a request that does gets 400
	// UNSUPPORTED_VISIBILITY_POLICY instead of a silently wider audience.
	VisibilityPolicy json.RawMessage    `json:"visibility_policy,omitempty"`
	ContentType      string             `json:"content_type"`
	MediaIDs         []string           `json:"media_ids"`
	Feeling          *string            `json:"feeling"`
	Activity         *string            `json:"activity"`
	ActivityDetail   *string            `json:"activity_detail"`
	RichText         json.RawMessage    `json:"rich_text"`
	Poll             *CreatePollRequest `json:"poll"`
	NoComments       bool               `json:"no_comments"`
	NoLikes          bool               `json:"no_likes"`
	LocationName     *string            `json:"location_name"`
	LocationLat      *float64           `json:"location_lat"`
	LocationLng      *float64           `json:"location_lng"`
	PostType         string             `json:"post_type"`
	AppOrigin        string             `json:"app_origin"`
	// Presence-aware (fixes-v2 / Codex P1-1): a pointer distinguishes an
	// omitted field from an explicit `false`, so an old client's explicit
	// opt-out is honored. JSON compatibility is unchanged — the stored
	// column stays a plain bool, defaulting to true when omitted, which
	// matches the schema default.
	ShareToPostbook *bool `json:"share_to_postbook"`
	// Reel metadata
	Title string `json:"title"`
	// M5: cap tags array to bound payload memory. 20 tags × 50 chars
	// is the upper bound for legitimate use cases (most reels carry
	// 3-5 tags); larger arrays are either spam or accidents.
	Tags           []string `json:"tags" binding:"max=20,dive,max=50"`
	Category       string   `json:"category"`
	Language       string   `json:"language"`
	SEOTitle       string   `json:"seo_title"`
	PaidPromotion  bool     `json:"paid_promotion"`
	AlteredContent bool     `json:"altered_content"`
	IsMadeForKids  bool     `json:"is_made_for_kids"`
	License        string   `json:"license"`
	AllowEmbedding *bool    `json:"allow_embedding"`
	// PublishToFeed / ShareToPostbook are the pre-policy distribution
	// fields. Pointers so an EXPLICIT false from an old client is
	// distinguishable from "absent" and is honored (Codex P1-1).
	PublishToFeed     *bool   `json:"publish_to_feed"`
	RemixSetting      string  `json:"remix_setting"`
	CommentModeration string  `json:"comment_moderation"`
	CommentAccess     string  `json:"comment_access"`
	RecordingDate     *string `json:"recording_date"`
	RecordingLocation string  `json:"recording_location"`
	CoverMediaID      *string `json:"cover_media_id"`
	OriginalAudioVol  float32 `json:"original_audio_volume"`
	OverlayAudioVol   float32 `json:"overlay_audio_volume"`
	// AudioTrackID attaches a sound from media-service's /v1/audio to the post on
	// create. Used by the Flicks composer's audio browser. Optional —
	// posts without background audio leave this empty.
	AudioTrackID *string `json:"audio_track_id"`
	// AudioStartMs is where in that sound playback starts, in
	// milliseconds (2026-09-29). A value outside the sound is stored as 0.
	AudioStartMs *int `json:"audio_start_ms"`
	// Per-reel controls (2026-09-04). The four switches on the reel
	// composer are no_comments, hide_share, allow_download and
	// remix_setting. AllowDownload is presence-aware like ShareToPostbook:
	// the column defaults to true, so only an explicit false may turn it
	// off, and an old client that never sends the field keeps downloads on.
	// HideShare's absent-and-false agree, so a plain bool is enough.
	HideShare     bool  `json:"hide_share"`
	AllowDownload *bool `json:"allow_download"`
	// TaggedUserIDs is the composer's people picker. Strings, parsed and
	// bounded in parseTaggedUserIDs, deduped and author-stripped in the
	// service.
	TaggedUserIDs []string `json:"tagged_user_ids"`
	// PublishAt schedules the post (RFC3339, 5 min – 30 days out). Absent or
	// empty publishes now. Scheduled posts are author-only until the
	// worker publishes them (internal/postschedule).
	PublishAt *string `json:"publish_at"`
	// Hashtags and Mentions are the reel studio's separate fields
	// (2026-09-05), merged with what the caption parser extracts. Bounds and
	// alphabet are enforced by the service (explicit_tags.go).
	Hashtags []string `json:"hashtags"`
	Mentions []string `json:"mentions"`
	// Distribution is the typed, versioned scalar policy (Module 1 P0-1):
	// {"version":1,"main_feed":bool,"notify_subscribers":bool,
	//  "create_reel_preview":bool}. Omitted = legacy behavior. Unknown
	// fields, wrong version, or unsupported true flags → 400.
	Distribution json.RawMessage `json:"distribution"`

	// Post screens parity (2026-10-18, service/post_screen_fields.go).
	// omitempty on every one so a request that sends none of them
	// fingerprints exactly as it did before (create_fingerprint.go) and an
	// in-flight retry across the deploy still replays.
	//
	//   cover_index         0-based position in media_ids of a photo post's
	//                       cover; must agree with cover_media_id when both.
	//   is_branded          content disclosure next to paid_promotion.
	//   ai_generated        "made with AI" label.
	//   link_url            https only, <= 500 chars ("" = none).
	//   location_place_id   the place provider's id for location_name.
	CoverIndex      *int    `json:"cover_index,omitempty"`
	IsBranded       bool    `json:"is_branded,omitempty"`
	AIGenerated     bool    `json:"ai_generated,omitempty"`
	LinkURL         string  `json:"link_url,omitempty"`
	LocationPlaceID *string `json:"location_place_id,omitempty"`

	// Video credits (2026-10-23, service/credits.go), long videos only;
	// omitempty for the same fingerprint reason as the block above.
	//
	//   credits           [{name, role, user_id?}], max 30, order kept.
	//   genres            up to 3 slugs from GET /v1/posts/genres.
	//   release_year      1900 … next year.
	//   production_house  <= 100 characters.
	Credits         []postgres.CreditInput `json:"credits,omitempty"`
	Genres          []string               `json:"genres,omitempty"`
	ReleaseYear     *int                   `json:"release_year,omitempty"`
	ProductionHouse string                 `json:"production_house,omitempty"`

	// TierRequiredID gates the post on one of the author's active membership
	// tiers from birth (2026-10-23, service/members_tiers.go), for every
	// content type — so a post meant for members is never public for a
	// moment, as it was when the client created it and then PUT the gate.
	// A UUID string; absent or null = open. Not one of the author's active
	// tiers: 400 INVALID_TIER. Tiers unreadable: 503
	// MEMBERSHIP_TIERS_UNAVAILABLE and no post. omitempty for the
	// fingerprint reason above.
	TierRequiredID *uuid.UUID `json:"tier_required_id,omitempty"`

	// Action is the Action button (2026-10-23, action_links.go):
	// {"label","url"}; absent or null = none. Label, URL rules, the
	// author's eligibility and the safety check all run before anything is
	// written. omitempty for the fingerprint reason above.
	Action *service.ActionInput `json:"action,omitempty"`
}

// createPostResponse is the create body: the post as before, plus a long
// video's credits as its owner sees them. credits is absent on every other
// kind, so no other create response changes.
type createPostResponse struct {
	*postgres.Post
	Credits *[]service.CreditView `json:"credits,omitempty"`
}

// creditsMeta is the meta of a write that may have stored tags as plain
// text over the uploader's daily limit (service/credits.go).
type creditsMeta struct {
	RequestID         string `json:"request_id,omitempty"`
	NextCursor        string `json:"next_cursor,omitempty"`
	CreditTagsSkipped int    `json:"credit_tags_skipped,omitempty"`
}

// writeWithCreditsMeta answers data in the api.JSON envelope, adding
// meta.credit_tags_skipped when the write skipped any tags. With none
// skipped the bytes are exactly api.JSON's.
func writeWithCreditsMeta(c *gin.Context, status int, data any, skipped int) {
	if skipped <= 0 {
		api.JSON(c.Writer, status, data, nil)
		return
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(status)
	_ = json.NewEncoder(c.Writer).Encode(struct {
		Data any         `json:"data"`
		Meta creditsMeta `json:"meta"`
	}{Data: data, Meta: creditsMeta{CreditTagsSkipped: skipped}})
}

// writeDistributionError maps distribution policy errors to their typed
// HTTP responses; returns false when err is not a distribution error.
// maxCreatePostBodyBytes caps a create-post body before JSON binding.
//
// 256 KiB. The largest legal Slice C payload is ~20 KiB (5,000 code points at
// up to 4 bytes each, plus one media UUID, the distribution object and field
// names), so this leaves an order of magnitude of headroom for the richer
// fields the shared route still serves, while staying small enough that a flood
// of over-limit requests cannot pressure memory.
//
// Deliberately NOT the middleware's 8 KiB comment cap, which is sized for a
// comment body and would reject a legitimate long post.
const maxCreatePostBodyBytes = 256 * 1024

// writeCreateGuardError maps the Slice C create guards onto stable HTTP codes.
//
// Every one of these is a 4xx: they are all statements about the request, and
// returning 500 for a rejected attachment would tell the client to retry
// something that can never succeed (C-LB-1.3, C-LB-4.2).
func writeCreateGuardError(c *gin.Context, err error) bool {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, service.ErrEmptyPost):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "EMPTY_POST", err.Error(), nil)
	case errors.Is(err, service.ErrTextTooLong):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "TEXT_TOO_LONG", err.Error(), nil)
	case errors.Is(err, service.ErrCreateKeyReused):
		// 409, not 400: the request is well-formed, it conflicts with an
		// earlier one. The client must mint a new key, not fix a field.
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, "IDEMPOTENCY_KEY_REUSED", err.Error(), nil)
	case writeMediaClaimsError(c, err):
		// 503: media-service could not record the cover's claim; nothing
		// was written (service/media_claims.go).
	case errors.Is(err, service.ErrMediaNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "MEDIA_NOT_FOUND", err.Error(), nil)
	case errors.Is(err, service.ErrMediaNotOwned):
		// 403 and a message that discloses nothing about the asset.
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, "MEDIA_NOT_OWNED",
			"You cannot attach this media", nil)
	case errors.Is(err, service.ErrMediaNotReady):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "MEDIA_NOT_READY", err.Error(), nil)
	case errors.Is(err, service.ErrMediaTypeMismatch):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "MEDIA_TYPE_MISMATCH", err.Error(), nil)
	case errors.Is(err, service.ErrInvalidCategory):
		// 422, not 400 (2026-09-27): the body parses, one value is outside
		// the taxonomy. The code is unchanged; no client keys on the status.
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnprocessableEntity, "INVALID_CATEGORY", err.Error(), nil)
	case errors.Is(err, service.ErrTooManyTaggedUsers):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "TOO_MANY_TAGGED_USERS",
			fmt.Sprintf("a post may tag at most %d people", service.MaxTaggedUsers), nil)
	case errors.Is(err, service.ErrTitleRequired):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "TITLE_REQUIRED", err.Error(), nil)
	case errors.Is(err, service.ErrTitleTooLong):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "TITLE_TOO_LONG", err.Error(), nil)
	case errors.Is(err, service.ErrInvalidContentType):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_CONTENT_TYPE", err.Error(), nil)
	case errors.Is(err, service.ErrChannelRequired):
		// Tube: a long video needs a channel first (founder rule, 2026-09-05).
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, "CHANNEL_REQUIRED", "Create your channel before posting a video", nil)
	// Post screens parity (2026-10-18): statements about the request, 400.
	case errors.Is(err, service.ErrInvalidCover):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_COVER", err.Error(), nil)
	case errors.Is(err, service.ErrInvalidRemixSetting):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REMIX_SETTING", err.Error(), nil)
	case errors.Is(err, service.ErrInvalidCommentAccess):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_COMMENT_ACCESS", err.Error(), nil)
	case errors.Is(err, service.ErrInvalidLinkURL):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_LINK_URL", err.Error(), nil)
	case errors.Is(err, service.ErrInvalidLocationPlaceID):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_LOCATION_PLACE_ID", err.Error(), nil)
	case errors.Is(err, service.ErrInvalidVisibility):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_VISIBILITY", err.Error(), nil)
	case writeTierGateError(c, err):
	case writeCreditFieldError(c, err):
	case writeActionError(c, err):
	default:
		return false
	}
	return true
}

// writeCreditFieldError answers the video-credit field refusals (2026-10-23)
// with the codes PATCH answers (service.PostEditErrorStatus), all 400; false
// when err is none of them.
func writeCreditFieldError(c *gin.Context, err error) bool {
	switch {
	case errors.Is(err, service.ErrCreditsNotAllowed), errors.Is(err, service.ErrTooManyCredits),
		errors.Is(err, service.ErrInvalidCredit), errors.Is(err, service.ErrInvalidGenre),
		errors.Is(err, service.ErrInvalidReleaseYear), errors.Is(err, service.ErrInvalidProductionHouse):
		status, code := service.PostEditErrorStatus(err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, status, code, err.Error(), nil)
		return true
	}
	return false
}

// resolveAllowDownload is the presence-aware default for `allow_download`.
// The column defaults TRUE; only an explicit false turns downloads off.
func resolveAllowDownload(explicit *bool) bool {
	if explicit == nil {
		return true
	}
	return *explicit
}

// parseTaggedUserIDs turns the request's strings into ids.
//
// The cap is checked here as well as in the service so a 21-id request fails
// before twenty-one parses, and so the error names the limit rather than a
// UUID. Dedupe and dropping the author stay in the service, where every
// creation path shares them.
func parseTaggedUserIDs(raw []string) ([]uuid.UUID, error) {
	if len(raw) > service.MaxTaggedUsers {
		return nil, fmt.Errorf("a post may tag at most %d people", service.MaxTaggedUsers)
	}
	ids := make([]uuid.UUID, 0, len(raw))
	for _, s := range raw {
		id, err := uuid.Parse(strings.TrimSpace(s))
		if err != nil {
			return nil, fmt.Errorf("invalid tagged user id: %s", s)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (h *Handler) CreatePost(c *gin.Context) {
	authorIDStr := c.GetHeader("X-User-Id")
	authorID, err := uuid.Parse(authorIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	// Post creation rate limit (Redis sliding window)
	if err := service.CheckPostRateLimit(c.Request.Context(), h.rdb, authorID); err != nil {
		c.Header("Retry-After", "3600")
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", err.Error(), nil)
		return
	}

	// DURABLE CREATION KEY (C-LB-3.1).
	//
	// Required and must parse as a UUID. Required, because a create without one
	// cannot be made exactly-once and "server committed, response lost" is the
	// normal outcome of publishing from a phone. A UUID specifically, because an
	// attacker-chosen or colliding key is a way to read back or clobber another
	// intent, and because it gives the client no reason to invent a scheme.
	createKey := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if createKey == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			"MISSING_IDEMPOTENCY_KEY", "Idempotency-Key header is required", nil)
		return
	}
	if _, err := uuid.Parse(createKey); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			"INVALID_IDEMPOTENCY_KEY", "Idempotency-Key must be a UUID", nil)
		return
	}

	// BODY CAP BEFORE BIND (C-LB-1.4).
	//
	// `MaxBytesReader` wraps the body itself, so the limit is enforced as the
	// JSON decoder reads. A `Content-Length` pre-check is not equivalent: a
	// chunked request carries no length, and a hostile one can simply lie.
	// Capping the reader means an over-limit body is cut off mid-decode and
	// nothing is ever written.
	//
	// The ceiling is far above the largest legal P0 payload (5,000 code points
	// of text, at most 4 bytes each, plus one media UUID and a small policy
	// object) and far below anything that could pressure memory.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxCreatePostBodyBytes)

	var req CreatePostRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusRequestEntityTooLarge,
				"PAYLOAD_TOO_LARGE", "Request body exceeds the maximum size", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	// visibility_policy was never applied (see the field): refuse it rather
	// than publish to an audience the client did not describe. JSON null
	// is the same as absent.
	if p := bytes.TrimSpace(req.VisibilityPolicy); len(p) > 0 && !bytes.Equal(p, []byte("null")) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "UNSUPPORTED_VISIBILITY_POLICY",
			"visibility_policy is not supported; use visibility (public, followers, friends, private, unlisted) and private shares", nil)
		return
	}

	// An unknown content_type is refused before any lookup; the service
	// refuses it too (ErrInvalidContentType) for every other caller.
	if req.ContentType != "" {
		if _, ok := service.CanonicalContentType(req.ContentType); !ok {
			writeCreateGuardError(c, fmt.Errorf("%w %q: must be post, poll, flick, or long_video",
				service.ErrInvalidContentType, req.ContentType))
			return
		}
	}

	// Validate media IDs
	if len(req.MediaIDs) > 10 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Maximum 10 media attachments", nil)
		return
	}

	var mediaIDs []uuid.UUID
	seenMedia := make(map[uuid.UUID]struct{}, len(req.MediaIDs))
	for _, idStr := range req.MediaIDs {
		id, err := uuid.Parse(idStr)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Invalid media ID: "+idStr, nil)
			return
		}
		// Reject duplicates BEFORE the insert. `post_media` is keyed on
		// (post_id, media_id), so a repeated id would be silently collapsed and
		// a three-image carousel would publish as two with no error returned to
		// anyone. Creator Studio P0-A, freeze-v3 2.3.
		if _, dup := seenMedia[id]; dup {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "DUPLICATE_MEDIA", "Media ID repeated in one post: "+idStr, nil)
			return
		}
		seenMedia[id] = struct{}{}
		mediaIDs = append(mediaIDs, id)
	}

	// Parse optional recording date
	var recordingDate *time.Time
	if req.RecordingDate != nil && *req.RecordingDate != "" {
		if t, err := time.Parse("2006-01-02", *req.RecordingDate); err == nil {
			recordingDate = &t
		}
	}

	// Parse optional cover media ID
	var coverMediaID *uuid.UUID
	if req.CoverMediaID != nil {
		if id, err := uuid.Parse(*req.CoverMediaID); err == nil {
			coverMediaID = &id
		} else if strings.EqualFold(strings.TrimSpace(req.PostType), "image") && strings.TrimSpace(*req.CoverMediaID) != "" {
			// A photo post's cover must be one of its pages (2026-10-18);
			// a value that is not even an id cannot be, and is not dropped.
			writeCreateGuardError(c, service.ErrInvalidCover)
			return
		}
	}

	// Default booleans for optional pointer fields
	allowEmbedding := true
	if req.AllowEmbedding != nil {
		allowEmbedding = *req.AllowEmbedding
	}
	publishToFeed := true
	if req.PublishToFeed != nil {
		publishToFeed = *req.PublishToFeed
	}
	// share_to_postbook column default is TRUE; an omitted field keeps it.
	shareToPostbook := true
	if req.ShareToPostbook != nil {
		shareToPostbook = *req.ShareToPostbook
	}

	taggedUserIDs, err := parseTaggedUserIDs(req.TaggedUserIDs)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			"INVALID_REQUEST", err.Error(), nil)
		return
	}

	publishAt, err := parsePublishAt(req.PublishAt)
	if err != nil {
		writeScheduleError(c, err)
		return
	}

	// The fingerprint is taken over the WHOLE accepted request (C-P0-5).
	// Computed before anything is written: a request whose canonical form
	// cannot be produced cannot be safely bound to an idempotency key, and
	// guessing one would let a different body replay an earlier post.
	fingerprint, err := createFingerprint(req)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			"INVALID_REQUEST", err.Error(), nil)
		return
	}

	input := &service.CreatePostInput{
		AuthorID:          authorID,
		Text:              req.Text,
		Visibility:        req.Visibility,
		ContentType:       req.ContentType,
		MediaIDs:          mediaIDs,
		Feeling:           req.Feeling,
		Activity:          req.Activity,
		ActivityDetail:    req.ActivityDetail,
		RichText:          req.RichText,
		NoComments:        req.NoComments,
		NoLikes:           req.NoLikes,
		LocationName:      req.LocationName,
		LocationLat:       req.LocationLat,
		LocationLng:       req.LocationLng,
		PostType:          req.PostType,
		AppOrigin:         req.AppOrigin,
		ShareToPostbook:   shareToPostbook,
		Title:             req.Title,
		Tags:              req.Tags,
		Category:          req.Category,
		Language:          req.Language,
		SEOTitle:          req.SEOTitle,
		PaidPromotion:     req.PaidPromotion,
		AlteredContent:    req.AlteredContent,
		IsMadeForKids:     req.IsMadeForKids,
		License:           req.License,
		AllowEmbedding:    allowEmbedding,
		PublishToFeed:     publishToFeed,
		RemixSetting:      req.RemixSetting,
		CommentModeration: req.CommentModeration,
		CommentAccess:     req.CommentAccess,
		RecordingDate:     recordingDate,
		RecordingLocation: req.RecordingLocation,
		CoverMediaID:      coverMediaID,
		OriginalAudioVol:  req.OriginalAudioVol,
		OverlayAudioVol:   req.OverlayAudioVol,
		HideShare:         req.HideShare,
		AllowDownload:     resolveAllowDownload(req.AllowDownload),
		TaggedUserIDs:     taggedUserIDs,
		Distribution:      req.Distribution,
		PublishAt:         publishAt,
		Hashtags:          req.Hashtags,
		Mentions:          req.Mentions,
		// P1-1: forward the explicit legacy intent so an old client's
		// `publish_to_feed:false` is honored instead of silently
		// overridden by the canonical default.
		LegacyDistribution: service.LegacyDistributionFields{
			PublishToFeed:   req.PublishToFeed,
			ShareToPostbook: req.ShareToPostbook,
		},
		// `visibility` is binding:"required" on this route, so anything that
		// reaches here chose its audience deliberately. That is what stops the
		// after-hours rule silently rewriting an explicit Public to `trusted`
		// (C-LB-2.3).
		VisibilityExplicit: true,
		CreateKey:          createKey,
		CreateFingerprint:  fingerprint,
		CoverIndex:         req.CoverIndex,
		IsBranded:          req.IsBranded,
		AIGenerated:        req.AIGenerated,
		LinkURL:            req.LinkURL,
		LocationPlaceID:    req.LocationPlaceID,
		VideoCredits: service.VideoCreditFields{
			Credits: req.Credits, Genres: req.Genres, ReleaseYear: req.ReleaseYear, ProductionHouse: req.ProductionHouse,
		},
		TierRequiredID: req.TierRequiredID,
		Action:         req.Action,
	}

	if req.Poll != nil {
		input.Poll = &service.CreatePollInput{
			Question:       req.Poll.Question,
			Options:        req.Poll.Options,
			AllowsMultiple: req.Poll.AllowsMultiple,
			DurationHours:  req.Poll.DurationHours,
		}
	}

	p, err := h.svc.CreatePost(c.Request.Context(), input)
	if err != nil {
		if writeStandingError(c, err) {
			return
		}
		if writeCoverMediaError(c, err) {
			return
		}
		if writeCreateGuardError(c, err) {
			return
		}
		if writeScheduleError(c, err) {
			return
		}
		if writeDistributionError(c, err) {
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	// Attach background audio if the client picked a track. Best-effort:
	// failure here logs but doesn't fail the create — the post is already
	// persisted and a missing audio reference is recoverable from the UI.
	if audioID, startMs, ok := requestedSound(&req); ok {
		// M10: pass authorID so the service enforces the private-
		// track ownership check. Failure is logged at slog.Warn
		// (a private-track refusal shouldn't bring down the
		// post; the post is already persisted).
		link, err := h.svc.AttachAudioToPost(c.Request.Context(), authorID, p.ID, audioID, startMs)
		if err != nil {
			slog.Warn("create-post: attach audio failed",
				"post_id", p.ID, "audio_id", audioID, "err", err)
		}
		// The body answers what was stored: the sound and its start.
		service.ApplySoundLink(p, link)
	}

	// A long video answers with its credits as the owner sees them, and
	// meta.credit_tags_skipped when the tagging limit turned tags into
	// plain text.
	resp, skipped := h.svc.CreatedPostWithCredits(c.Request.Context(), p)
	if resp.Credits == nil {
		writeWithCreditsMeta(c, http.StatusCreated, p, skipped)
		return
	}
	writeWithCreditsMeta(c, http.StatusCreated, createPostResponse{Post: p, Credits: resp.Credits}, skipped)
}

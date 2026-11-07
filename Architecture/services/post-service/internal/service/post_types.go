// post_types.go holds the shapes a post travels in through the service:
// PostDetail (what every read returns) and CreatePostInput /
// CreatePollInput (what a create carries). Moved out of post.go unchanged.

package service

import (
	"encoding/json"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/post-service/internal/store/scylla"
	"github.com/google/uuid"
)

type PostDetail struct {
	*postgres.Post
	Counts         *scylla.Counts     `json:"counts"`
	ViewCount      int64              `json:"view_count"`
	ViewerReaction *string            `json:"viewer_reaction,omitempty"`
	HasReacted     bool               `json:"has_reacted"`
	IsBookmarked   bool               `json:"is_bookmarked"`
	RepostCount    int                `json:"repost_count"`
	ViewerRepost   *RepostStateResult `json:"viewer_repost,omitempty"`
	HasReposted    bool               `json:"has_reposted"`
	IsRepostable   bool               `json:"is_repostable"`
	// Channel is the author's Tube channel, attached to long_video posts
	// whose author has one (channels.go); omitted otherwise.
	Channel *ChannelRef `json:"channel,omitempty"`
	// Author is the same block the feed carries, from the same source
	// (identity-profile). Absent when the profile lookup is unavailable.
	Author *PostAuthor `json:"author,omitempty"`
	// MTube (2026-09-27), never omitempty so a client cannot confuse
	// "false" with "unknown": ViewerDisliked is the viewer's private tune
	// (no count exists anywhere); ViewerQueued is "in the viewer's Queue"
	// (watch_later system collection).
	ViewerDisliked bool `json:"viewer_disliked"`
	ViewerQueued   bool `json:"viewer_queued"`
	// Chapters: saved media_chapters, or for a long video the chapters
	// derived from description timestamps (chapters.go). Always an array
	// on the direct read (GET /v1/posts/:id); null on list surfaces, which
	// do not load chapters.
	Chapters []ChapterRef `json:"chapters"`

	// Creator Hub (2026-09-28). LikeCount is the like number when the
	// author hid it (hide_like_count): null for everyone but the owner, and
	// counts.likes is 0 for them too. The direct read always sets it; list
	// surfaces set it only when hidden (absent otherwise). RelatedPost is
	// the owner-picked related post as a card, on the direct read only and
	// only when this viewer may see it (else null, and related_post_id is
	// null too). NotifySubscribers is the owner's (resolved distribution)
	// and absent for everyone else.
	LikeCount         *LikeCount        `json:"like_count,omitempty"`
	RelatedPost       *RelatedPostField `json:"related_post,omitempty"`
	NotifySubscribers *bool             `json:"notify_subscribers,omitempty"`
	// Restrictions (migration 056) is the owner's view of every active
	// case-specific restriction on the direct read: present (possibly
	// empty) for the owner, absent for everyone else — a viewer never
	// reaches this struct for a restricted post at all.
	Restrictions []RestrictionNotice `json:"restrictions,omitempty"`
	// CopyrightReview is the author's view of a copyright title review
	// (copyright_title_review.go): on the owner's direct read and uploads
	// rows of a long video that was flagged, absent otherwise and for every
	// other viewer. Per viewer, so never in the cached body.
	CopyrightReview *CopyrightReviewView `json:"copyright_review,omitempty"`
	// Sound is the added sound the post plays (audio_track_id), for a
	// viewer who may hear it; absent when there is none, when this viewer
	// may not, and when that could not be decided (sounds.go). It is on the
	// detail and not on the post because the post body is what the cache
	// holds (post_cache.go), and a cached body carries nothing per viewer.
	Sound *PostSound `json:"sound,omitempty"`
	// Credits is a long video's cast & crew as this viewer may see them
	// (credits.go): an array on the direct read of a long video (possibly
	// empty), absent for every other kind and on list surfaces. Per viewer,
	// so on the detail and never in the cached post body.
	Credits *[]CreditView `json:"credits,omitempty"`
	// CreditTagsSkipped is how many tags the write behind this detail stored
	// as plain text over the uploader's daily limit; the handler puts it in
	// meta.credit_tags_skipped. Never in the body.
	CreditTagsSkipped int `json:"-"`
}

// CreatePostInput holds all fields for creating a new post.
type CreatePostInput struct {
	// PostID, when set, is used as the new post's id instead of a random
	// UUID. Draft publishing passes the draft id here so a crash-retry
	// hits the posts PK instead of double-publishing (P0-5 idempotency).
	PostID          *uuid.UUID
	AuthorID        uuid.UUID
	Text            string
	Visibility      string
	ContentType     string
	MediaIDs        []uuid.UUID
	Feeling         *string
	Activity        *string
	ActivityDetail  *string
	RichText        json.RawMessage
	Poll            *CreatePollInput
	NoComments      bool
	NoLikes         bool
	LocationName    *string
	LocationLat     *float64
	LocationLng     *float64
	PostType        string
	AppOrigin       string
	ShareToPostbook bool
	// Reel metadata
	Title             string
	Tags              []string
	Category          string
	Language          string
	SEOTitle          string
	PaidPromotion     bool
	AlteredContent    bool
	IsMadeForKids     bool
	License           string
	AllowEmbedding    bool
	PublishToFeed     bool
	RemixSetting      string
	CommentModeration string
	CommentAccess     string
	RecordingDate     *time.Time
	RecordingLocation string
	CoverMediaID      *uuid.UUID
	OriginalAudioVol  float32
	OverlayAudioVol   float32
	// Per-reel controls (2026-09-04). AllowDownload is a plain bool like
	// ShareToPostbook: the HANDLER resolves an omitted field to true, so
	// every other constructor of this input must set it deliberately.
	HideShare     bool
	AllowDownload bool
	// TaggedUserIDs is already parsed; NormalizeTaggedUsers decides what
	// is stored.
	TaggedUserIDs []uuid.UUID
	// PublishAt schedules the post (schedule.go): nil publishes now; a
	// time in [now+5m, now+30d] stores it author-only until the worker
	// publishes it. Validated by ValidatePublishAt before anything is written.
	PublishAt *time.Time
	// Hashtags and Mentions are the studio's separate fields
	// (explicit_tags.go), merged with what the caption parser extracts.
	Hashtags []string
	Mentions []string
	// Distribution is the raw policy document from the client (P0-1).
	// nil = no policy = legacy behavior. Validated by ParseDistributionPolicy.
	Distribution json.RawMessage
	// LegacyDistribution carries explicitly-supplied pre-policy fields
	// from old clients. When no typed policy is present these express the
	// creator's real intent and are honored (Codex P1-1).
	LegacyDistribution LegacyDistributionFields

	// VisibilityExplicit records that the CALLER chose this audience, as
	// opposed to inheriting a default (Slice C, C-LB-2).
	//
	// The after-hours Trusted Circle rule below could not tell the two apart:
	// it matched on the VALUE "public", which a defaulting client and a
	// deliberate client produce identically. Its own comment says a manually
	// selected wider audience should win, and the implementation rewrote both.
	// A user who explicitly published to Public at 23:00 got a trusted-circle
	// post and was told it was public.
	//
	// The HTTP handler sets this because `visibility` is `binding:"required"`
	// there — every request over the wire is an explicit choice. Internal
	// callers that genuinely default leave it false and keep the old behaviour.
	VisibilityExplicit bool

	// CreateKey and CreateFingerprint carry the durable idempotency claim
	// (C-LB-3). Empty CreateKey disables the claim, which is what internal
	// callers (draft publish, thread entries) do — they have their own
	// exactly-once mechanism through PostID.
	CreateKey         string
	CreateFingerprint string

	// Post screens parity (2026-10-18, post_screen_fields.go). CoverIndex
	// picks the cover of a photo post by position in MediaIDs; LinkURL and
	// LocationPlaceID are raw ("" = none) and normalized by CreatePost.
	CoverIndex      *int
	IsBranded       bool
	AIGenerated     bool
	LinkURL         string
	LocationPlaceID *string

	// Video credits (2026-10-23, credits.go), long videos only. Validated
	// and linked by CreatePost; written in the post's transaction.
	VideoCredits VideoCreditFields

	// TierRequiredID gates the post on one of the author's membership tiers
	// from birth (members_tiers.go). nil = open to everyone. CreatePost
	// checks it against monetization-service before anything is written and
	// stores it on the row in the post's own transaction, so the post is
	// never public for a moment and its PostCreated already carries the gate.
	TierRequiredID *uuid.UUID

	// Action is the Action button (action_links.go); nil = none. CreatePost
	// runs the label / URL / eligibility / safety checks before anything is
	// written and inserts the link in the post's own transaction.
	Action *ActionInput
}

// CreatePollInput holds poll creation data.
type CreatePollInput struct {
	Question       string
	Options        []string
	AllowsMultiple bool
	DurationHours  *int
}

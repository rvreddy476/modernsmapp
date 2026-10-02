package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

/*
	Offline copies inside the app, never a file download (2026-10-02).

	A viewer never receives a file. "Save offline" stores a video in the
	app's own private storage and plays it only inside the app; the server's
	side of that is four routes and one table (migration 060):

	  POST   /v1/posts/:postId/offline   {device_id}            grant / refresh
	  POST   /v1/posts/offline/check     {device_id, post_ids}  still valid?
	  GET    /v1/posts/offline?device_id=                        the device's copies
	  DELETE /v1/posts/:postId/offline   {device_id} | ?device_id=  remove

	Who may be granted a copy. In this order:

	  1. the post exists                                   else 404
	  2. the viewer may WATCH it — postMediaDecisions, the
	     decision playback itself is made by (account
	     privacy, hidden authors, review state, schedule,
	     blocks and mutes, audience, private shares, the
	     18+ gate); a post still processing is its
	     author's alone, as on every read                  else 404
	  3. it is a long video or a reel                      else 422
	  4. the creator allows it (allow_download), or the
	     caller is the creator                             else 403
	  5. a members-only post: the viewer is entitled       else 403
	  6. it is published and processed (the owner's own
	     scheduled / processing post lands here)           else 409 NOT_READY
	  7. a 720p, 480p or 360p rendition exists; the
	     author's original upload is never handed out      else 409 NOT_READY
	  8. the viewer holds fewer than 100 active copies     else 409 OFFLINE_LIMIT

	The bytes are not this service's: the card names media-service's serve
	route, which makes the audience decision again on every request and
	redirects to a short-lived signed URL with no attachment disposition.

	The stored row is never the whole answer. The check (and the list) make
	the same decision again from the post as it stands — deleted, no longer
	the viewer's to watch, blocked, downloads turned off — and only then read
	revoked_at and expires_at. A dependency that cannot answer is a 503, not
	an "invalid": a client deletes the file when told a copy is invalid, and
	an outage must never do that.
*/

const (
	// OfflineCopyTTL is how long a grant lasts; a repeated grant restarts it.
	OfflineCopyTTL = 30 * 24 * time.Hour
	// OfflineRecheckAfterSeconds is how long a client may go without
	// calling the check before it should treat its copies as unverified.
	OfflineRecheckAfterSeconds = 172800
	// MaxActiveOfflineCopies is the most active copies one user may hold,
	// across all their devices.
	MaxActiveOfflineCopies = 100
	// MaxOfflineCheckIDs caps one check request.
	MaxOfflineCheckIDs = 100
	// MaxOfflineDeviceIDLen is the longest device id (characters).
	MaxOfflineDeviceIDLen = 64

	offlineSoundMime      = "audio/mp4" // every sound is AAC in an M4A container (media-service)
	offlineCaptionTimeout = 2 * time.Second
	offlineMediaTimeout   = 3 * time.Second
	maxOfflineCaptionBody = 4 << 20
)

// offlineVariants is the preference order of the rendition a copy is made
// from. The author's original is deliberately absent: it is never a copy.
var offlineVariants = []string{"720p", "480p", "360p"}

// offlinePosterVariants is the preference order of the still a card shows
// when the post has no cover image of its own.
var offlinePosterVariants = []string{"thumb_300", "thumb_150"}

var (
	// ErrOfflineNotAllowed: the creator has not allowed saving this video
	// offline, or the viewer is not entitled to a members-only video.
	ErrOfflineNotAllowed = errors.New("saving this video offline is not allowed")
	// ErrOfflineUnsupported: the post is not a long video or a reel, or it
	// carries no video.
	ErrOfflineUnsupported = errors.New("only a video or a reel can be saved offline")
	// ErrOfflineNotReady: scheduled, still processing, or no rendition yet.
	ErrOfflineNotReady = errors.New("this video is not ready to be saved offline yet")
	// ErrOfflineLimit: the viewer already holds MaxActiveOfflineCopies.
	ErrOfflineLimit = fmt.Errorf("you can keep at most %d videos offline", MaxActiveOfflineCopies)
	// ErrOfflineDevice: device_id is missing, too long or not printable.
	ErrOfflineDevice = fmt.Errorf("device_id is required (at most %d characters)", MaxOfflineDeviceIDLen)
	// ErrOfflineTooMany: more than MaxOfflineCheckIDs ids in one check.
	ErrOfflineTooMany = fmt.Errorf("at most %d post ids per check", MaxOfflineCheckIDs)
	// ErrOfflineUnavailable: something the decision depends on did not
	// answer. Retryable; never read as "not allowed" or "invalid".
	ErrOfflineUnavailable = errors.New("offline copies are unavailable right now")
)

// Reasons an offline copy is no longer valid (the check's `reason`).
const (
	OfflineReasonDeleted    = "deleted"
	OfflineReasonPrivate    = "private"
	OfflineReasonNotAllowed = "not_allowed"
	OfflineReasonExpired    = "expired"
	OfflineReasonBlocked    = "blocked"
	OfflineReasonRevoked    = "revoked"
	OfflineReasonUnknown    = "unknown"
)

// offlineStore is the storage slice the offline routes need
// (store/postgres/offline_copies.go, posts.go).
type offlineStore interface {
	GetPost(ctx context.Context, id uuid.UUID) (*postgres.Post, error)
	GetPostsByIDs(ctx context.Context, ids []uuid.UUID) ([]postgres.Post, error)
	GrantOfflineCopy(ctx context.Context, userID, postID uuid.UUID, deviceID string, now, expiresAt time.Time, limit int, card json.RawMessage) (*postgres.OfflineCopy, bool, error)
	OfflineCopiesForPosts(ctx context.Context, userID uuid.UUID, deviceID string, postIDs []uuid.UUID) (map[uuid.UUID]postgres.OfflineCopy, error)
	ListActiveOfflineCopies(ctx context.Context, userID uuid.UUID, deviceID string, now time.Time) ([]postgres.OfflineCopy, error)
	TouchOfflineCopies(ctx context.Context, userID uuid.UUID, deviceID string, postIDs []uuid.UUID, now time.Time) error
	RevokeOfflineCopies(ctx context.Context, userID uuid.UUID, deviceID string, postIDs []uuid.UUID, reason string, now time.Time) (int64, error)
}

// offlineCaptionSource lists the caption tracks of a video that viewerID may
// read. Production asks media-service as that viewer (its own audience
// decision and its draft rule); tests substitute a fake.
type offlineCaptionSource interface {
	CaptionTracks(ctx context.Context, viewerID, mediaID uuid.UUID) ([]OfflineCaption, error)
}

// OfflineMedia is the rendition a copy is made from. Path is media-service's
// serve route, gateway-relative; it is on the grant only (the list carries
// the same record without it).
type OfflineMedia struct {
	MediaID uuid.UUID `json:"media_id"`
	Variant string    `json:"variant"`
	Path    string    `json:"path,omitempty"`
	Mime    string    `json:"mime"`
	// SizeBytes is the exact byte length of the rendition Path serves: the
	// length of the object the transcode worker uploaded, recorded with it
	// (media_variants.size_bytes, the value HEAD /serve/:variant answers as
	// Content-Length). Omitted when no length is recorded: a client that
	// verifies a copy against it must never be handed a guess.
	SizeBytes int64 `json:"size_bytes,omitempty"`
}

// OfflineCaption is one published caption track of the video.
type OfflineCaption struct {
	Lang  string `json:"lang"`
	Label string `json:"label"`
	Path  string `json:"path"`
}

// OfflineSound is the added sound a reel plays over its own video; the
// player plays both, so an offline copy needs both.
type OfflineSound struct {
	Path string `json:"path"`
	Mime string `json:"mime"`
	// SizeBytes is omitted today: no byte length is recorded for a sound,
	// and a wrong one would make a client discard a good copy.
	SizeBytes int64 `json:"size_bytes,omitempty"`
	// StartMs is where in the sound this post starts playing it.
	StartMs int `json:"start_ms"`
	// OriginalVolume and OverlayVolume are the post's own mix, the values
	// the reels player reads as original_audio_volume and
	// overlay_audio_volume: how loud the video's own audio and the added
	// sound play. Never omitted — 0 is "muted", not "unknown".
	OriginalVolume float32 `json:"original_volume"`
	OverlayVolume  float32 `json:"overlay_volume"`
}

// OfflineCard is the body of the grant and one row of the list.
type OfflineCard struct {
	PostID uuid.UUID `json:"post_id"`
	// ContentType is the post's, as stored: long_video | video for a long
	// video, flick | reel for a reel.
	ContentType         string           `json:"content_type"`
	ExpiresAt           time.Time        `json:"expires_at"`
	RecheckAfterSeconds int              `json:"recheck_after_seconds"`
	Title               string           `json:"title"`
	ChannelName         string           `json:"channel_name"`
	DurationMs          int              `json:"duration_ms"`
	PosterPath          string           `json:"poster_path"`
	Media               OfflineMedia     `json:"media"`
	Captions            []OfflineCaption `json:"captions"`
	Sound               *OfflineSound    `json:"sound"`
}

// offlineStoredCard is the part of a card the grant keeps on the row
// (post_offline_copies.grant_card): what the device was handed, so the list
// is rebuilt without asking media-service about every row. No storage key
// and no signed URL is ever in it.
type offlineStoredCard struct {
	Media      OfflineMedia     `json:"media"`
	Captions   []OfflineCaption `json:"captions"`
	Sound      *OfflineSound    `json:"sound"`
	PosterPath string           `json:"poster_path"`
}

// OfflineCheckItem is one answer of the check. PostID echoes the id as the
// client sent it. Reason is set only when Valid is false; ExpiresAt and
// ContentType only when it is true.
type OfflineCheckItem struct {
	PostID    string     `json:"post_id"`
	Valid     bool       `json:"valid"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Reason    string     `json:"reason,omitempty"`
	// ContentType is the post's, as on the card; on a valid copy only (an
	// invalid one may have no post left to read it from).
	ContentType string `json:"content_type,omitempty"`
	// Renewable is on a valid copy only: true when repeating the grant
	// (POST /v1/posts/:id/offline with the same device_id) would succeed
	// right now and move expires_at to 30 days from now, so a client can
	// skip a renewal that would be refused. See offlineRenewable for what
	// it is computed from.
	Renewable *bool `json:"renewable,omitempty"`
}

// OfflineRemoved is the body of DELETE /v1/posts/:postId/offline.
type OfflineRemoved struct {
	PostID  uuid.UUID `json:"post_id"`
	Removed bool      `json:"removed"`
}

// NormalizeOfflineDeviceID trims a device id and holds it to 1..64 printable,
// space-free characters. It is an opaque value the client made up; the
// server never interprets it.
func NormalizeOfflineDeviceID(raw string) (string, error) {
	id := strings.TrimSpace(raw)
	if id == "" || utf8.RuneCountInString(id) > MaxOfflineDeviceIDLen {
		return "", ErrOfflineDevice
	}
	for _, r := range id {
		if r == utf8.RuneError || unicode.IsSpace(r) || !unicode.IsPrint(r) {
			return "", ErrOfflineDevice
		}
	}
	return id, nil
}

// offlineContentType reports the kinds that can be saved offline: long
// videos and reels.
func offlineContentType(ct string) bool {
	switch ct {
	case "long_video", "video", "flick", "reel":
		return true
	}
	return false
}

// pickOfflineVariant chooses the rendition a copy is made from: 720p, else
// 480p, else 360p, the first that has an object behind it. The original
// upload is never a candidate. false when the asset is not a ready, passed
// video or has none of the three.
func pickOfflineVariant(mediaID uuid.UUID, rec *FeedMediaRecord) (OfflineMedia, bool) {
	if rec == nil || rec.FileType != "video" {
		return OfflineMedia{}, false
	}
	if !mediaPublishable(rec.ProcessingStatus, rec.ModerationStatus) {
		return OfflineMedia{}, false
	}
	for _, want := range offlineVariants {
		for _, v := range rec.Variants {
			if v.Name != want || v.ObjectKey == "" {
				continue
			}
			mime := strings.TrimSpace(v.Mime)
			if mime == "" {
				mime = feedDefaultMime
			}
			var size int64
			if v.SizeBytes != nil && *v.SizeBytes > 0 {
				size = *v.SizeBytes
			}
			return OfflineMedia{
				MediaID: mediaID, Variant: want,
				Path: "/v1/media/" + mediaID.String() + "/serve/" + want,
				Mime: mime, SizeBytes: size,
			}, true
		}
	}
	return OfflineMedia{}, false
}

// offlinePosterPath is the still a card shows: the post's own cover image,
// else a thumbnail the pipeline derived from the video, else "".
func offlinePosterPath(p *postgres.Post, videoID uuid.UUID, rec *FeedMediaRecord) string {
	if p != nil && p.CoverMediaID != nil {
		return "/v1/media/" + p.CoverMediaID.String() + "/serve"
	}
	if rec != nil {
		for _, want := range offlinePosterVariants {
			for _, v := range rec.Variants {
				if v.Name == want && v.ObjectKey != "" {
					return "/v1/media/" + videoID.String() + "/serve/" + want
				}
			}
		}
	}
	return ""
}

func (s *Service) offlineStore() offlineStore {
	if s.offline != nil {
		return s.offline
	}
	if s.pgStore != nil {
		return s.pgStore
	}
	return nil
}

// offlineEntitled answers the members-only rule (step 5). It is the lookup
// playback itself is gated on since 2026-10-02 (media_access.go
// viewerEntitled); the offline routes ask it as their own step, after the
// audience, so a non-member is told 403 "not allowed" rather than 404, and
// an outage is a 503 rather than a reason to delete a stored copy.
func (s *Service) offlineEntitled(ctx context.Context, viewerID uuid.UUID, p *postgres.Post) (bool, error) {
	return s.viewerEntitled(ctx, viewerID, p)
}

// GrantOfflineCopy grants (or refreshes) viewerID's copy of postID on
// deviceID. The bool is true when the grant made a copy active that was not
// (201), false when an active copy was refreshed (200). The refusals are in
// the file comment, in order.
func (s *Service) GrantOfflineCopy(ctx context.Context, viewerID, postID uuid.UUID, rawDeviceID string) (*OfflineCard, bool, error) {
	store := s.offlineStore()
	if store == nil {
		return nil, false, ErrOfflineUnavailable
	}
	if viewerID == uuid.Nil {
		return nil, false, ErrPostNotFound
	}
	deviceID, err := NormalizeOfflineDeviceID(rawDeviceID)
	if err != nil {
		return nil, false, err
	}
	p, err := store.GetPost(ctx, postID)
	if err != nil {
		return nil, false, fmt.Errorf("%w: load post: %v", ErrOfflineUnavailable, err)
	}
	if p == nil {
		return nil, false, ErrPostNotFound
	}
	if err := s.attachMediaState(ctx, []*postgres.Post{p}); err != nil {
		return nil, false, fmt.Errorf("%w: media state: %v", ErrOfflineUnavailable, err)
	}
	owner := p.AuthorID == viewerID

	// 2. May the viewer watch it? The playback decision, and nothing else.
	if !owner {
		decision, err := s.postMediaDecisions(ctx, viewerID, []*postgres.Post{p})
		if err != nil {
			return nil, false, fmt.Errorf("%w: audience: %v", ErrOfflineUnavailable, err)
		}
		// The audience half of the playback decision; its membership half
		// is step 5, with an answer of its own.
		if !decision.audience(p) {
			if !decision.resolved {
				return nil, false, fmt.Errorf("%w: account gate unresolved", ErrOfflineUnavailable)
			}
			// Private, followers-only, blocked either way, muted, hidden
			// author, pending review, scheduled, 18+ for a viewer who is
			// not: one answer, identical to a post that does not exist.
			return nil, false, ErrPostNotFound
		}
		if hiddenWhileProcessing(p, &viewerID) {
			return nil, false, ErrPostNotFound
		}
	}
	// 3. Long videos and reels only.
	if !offlineContentType(p.ContentType) {
		return nil, false, ErrOfflineUnsupported
	}
	// 4. The creator's switch; the creator needs no permission from it.
	if !owner && !p.AllowDownload {
		return nil, false, ErrOfflineNotAllowed
	}
	// 5. Members-only.
	if entitled, err := s.offlineEntitled(ctx, viewerID, p); err != nil {
		return nil, false, fmt.Errorf("%w: entitlement: %v", ErrOfflineUnavailable, err)
	} else if !entitled {
		return nil, false, ErrOfflineNotAllowed
	}
	// 6. Published and processed.
	if p.PublishAt != nil || p.IsProcessing {
		return nil, false, ErrOfflineNotReady
	}
	video := primaryVideo(p)
	if video == nil {
		return nil, false, ErrOfflineUnsupported
	}

	// 7. A rendition to copy.
	source := s.feedMedia
	if source == nil {
		source = httpFeedMediaSource{svc: s}
	}
	mediaCtx, cancel := context.WithTimeout(ctx, offlineMediaTimeout)
	rec, err := source.MediaRecord(mediaCtx, video.MediaID)
	cancel()
	if err != nil {
		return nil, false, fmt.Errorf("%w: media record: %v", ErrOfflineUnavailable, err)
	}
	media, ok := pickOfflineVariant(video.MediaID, rec)
	if !ok {
		return nil, false, ErrOfflineNotReady
	}

	stored := offlineStoredCard{
		Media:      media,
		Captions:   s.offlineCaptions(ctx, viewerID, video.MediaID),
		Sound:      s.offlineSound(ctx, viewerID, p),
		PosterPath: offlinePosterPath(p, video.MediaID, rec),
	}
	// The stored record never needs the path: it is derivable, and the list
	// does not hand it out.
	keep := stored
	keep.Media.Path = ""
	raw, err := json.Marshal(keep)
	if err != nil {
		return nil, false, fmt.Errorf("offline card: %w", err)
	}

	// 8. The limit, and the write.
	now := s.clock().UTC()
	row, created, err := store.GrantOfflineCopy(ctx, viewerID, postID, deviceID, now, now.Add(OfflineCopyTTL), MaxActiveOfflineCopies, raw)
	if err != nil {
		if errors.Is(err, postgres.ErrOfflineCopyLimit) {
			return nil, false, ErrOfflineLimit
		}
		return nil, false, fmt.Errorf("%w: grant: %v", ErrOfflineUnavailable, err)
	}

	card := s.offlineCard(p, row.ExpiresAt, stored, video.DurationMs, s.offlineChannelNames(ctx, viewerID, []*postgres.Post{p}))
	return card, created, nil
}

// offlineCard assembles a card from the post as it stands and the stored
// record of what was granted.
func (s *Service) offlineCard(p *postgres.Post, expiresAt time.Time, stored offlineStoredCard, durationMs int, channelNames map[uuid.UUID]string) *OfflineCard {
	title := strings.TrimSpace(p.Title)
	if title == "" {
		title = firstTextLine(p.Text)
	}
	captions := stored.Captions
	if captions == nil {
		captions = []OfflineCaption{}
	}
	poster := stored.PosterPath
	if p.CoverMediaID != nil {
		poster = "/v1/media/" + p.CoverMediaID.String() + "/serve"
	}
	return &OfflineCard{
		PostID: p.ID, ContentType: p.ContentType, ExpiresAt: expiresAt.UTC(), RecheckAfterSeconds: OfflineRecheckAfterSeconds,
		Title: title, ChannelName: channelNames[p.AuthorID], DurationMs: durationMs, PosterPath: poster,
		Media: stored.Media, Captions: captions, Sound: stored.Sound,
	}
}

// offlineChannelNames resolves what a card calls the author: the channel
// name when the author has a channel, else the profile's display name, else
// "". Best-effort: a card reads the same without it.
func (s *Service) offlineChannelNames(ctx context.Context, viewerID uuid.UUID, posts []*postgres.Post) map[uuid.UUID]string {
	out := map[uuid.UUID]string{}
	var authors []uuid.UUID
	for _, p := range posts {
		if p == nil {
			continue
		}
		if _, seen := out[p.AuthorID]; !seen {
			out[p.AuthorID] = ""
			authors = append(authors, p.AuthorID)
		}
	}
	if len(authors) == 0 {
		return out
	}
	if refs, err := s.ChannelRefsForUsers(ctx, viewerID, authors); err == nil {
		for id, ref := range refs {
			if ref != nil {
				out[id] = ref.Name
			}
		}
	}
	for _, id := range authors {
		if out[id] != "" {
			continue
		}
		if a := s.fetchPostAuthor(ctx, &viewerID, id); a != nil {
			out[id] = a.DisplayName
		}
	}
	return out
}

// offlineCaptions lists the caption tracks to store beside the video.
// Best-effort and never nil: a video is worth saving without its captions.
func (s *Service) offlineCaptions(ctx context.Context, viewerID, mediaID uuid.UUID) []OfflineCaption {
	source := s.offlineCaptionTracks
	if source == nil {
		source = httpOfflineCaptionSource{svc: s}
	}
	ctx, cancel := context.WithTimeout(ctx, offlineCaptionTimeout)
	defer cancel()
	tracks, err := source.CaptionTracks(ctx, viewerID, mediaID)
	if err != nil {
		slog.WarnContext(ctx, "offline copy: captions omitted", "media_id", mediaID, "err", err)
		return []OfflineCaption{}
	}
	if tracks == nil {
		return []OfflineCaption{}
	}
	return tracks
}

// offlineSound is the added sound the post plays, when this viewer may hear
// it (attachSounds: the same media-access decision every read applies).
func (s *Service) offlineSound(ctx context.Context, viewerID uuid.UUID, p *postgres.Post) *OfflineSound {
	if p == nil || p.AudioTrackID == nil {
		return nil
	}
	detail := &PostDetail{Post: p}
	s.attachSounds(ctx, &viewerID, []*PostDetail{detail})
	if detail.Sound == nil {
		return nil
	}
	return &OfflineSound{
		Path: "/v1/audio/" + detail.Sound.ID.String() + "/serve",
		Mime: offlineSoundMime, StartMs: detail.Sound.StartMs,
		OriginalVolume: p.OriginalAudioVol, OverlayVolume: p.OverlayAudioVol,
	}
}

// offlineJudge is the live half of "is this copy still valid" for one
// post: "" when the viewer could be granted it now, else the reason.
type offlineJudge func(p *postgres.Post) (reason string, err error)

// offlineJudgeFor resolves, once for a set of live posts, the decision the
// grant makes — may the viewer watch it, is it allowed, is the viewer
// entitled — and returns it per post. ErrOfflineUnavailable when the
// audience could not be decided.
func (s *Service) offlineJudgeFor(ctx context.Context, viewerID uuid.UUID, posts []*postgres.Post) (offlineJudge, error) {
	var others []*postgres.Post
	for _, p := range posts {
		if p != nil && p.AuthorID != viewerID {
			others = append(others, p)
		}
	}
	var decision *postMediaDecision
	if len(others) > 0 {
		var err error
		decision, err = s.postMediaDecisions(ctx, viewerID, others)
		if err != nil {
			return nil, fmt.Errorf("%w: audience: %v", ErrOfflineUnavailable, err)
		}
	}
	return func(p *postgres.Post) (string, error) {
		if p == nil || p.DeletedAt != nil {
			return OfflineReasonDeleted, nil
		}
		if p.AuthorID == viewerID {
			return "", nil
		}
		if !decision.audience(p) {
			if decision.blocked(p) {
				return OfflineReasonBlocked, nil
			}
			if !decision.resolved {
				// A denial the account gate produced because it could not
				// ask: not a fact about the post.
				return "", fmt.Errorf("%w: account gate unresolved", ErrOfflineUnavailable)
			}
			return OfflineReasonPrivate, nil
		}
		if !p.AllowDownload {
			return OfflineReasonNotAllowed, nil
		}
		entitled, err := s.offlineEntitled(ctx, viewerID, p)
		if err != nil {
			return "", fmt.Errorf("%w: entitlement: %v", ErrOfflineUnavailable, err)
		}
		if !entitled {
			return OfflineReasonNotAllowed, nil
		}
		return "", nil
	}, nil
}

// offlineValidity is one copy's answer: the live decision first, then the
// stored row.
func offlineValidity(row postgres.OfflineCopy, liveReason string, now time.Time) string {
	switch {
	case liveReason != "":
		return liveReason
	case row.RevokedAt != nil:
		return OfflineReasonRevoked
	case !row.ExpiresAt.After(now):
		return OfflineReasonExpired
	}
	return ""
}

// offlineRevokeReason maps a live refusal onto the reason written to the
// row; "" for the reasons that are already facts of the row.
func offlineRevokeReason(reason string) string {
	switch reason {
	case OfflineReasonDeleted:
		return postgres.OfflineRevokeDeleted
	case OfflineReasonPrivate:
		return postgres.OfflineRevokePrivate
	case OfflineReasonNotAllowed:
		return postgres.OfflineRevokeNotAllowed
	case OfflineReasonBlocked:
		return postgres.OfflineRevokeBlocked
	}
	return ""
}

// evaluateOfflineCopies decides every row against the post as it stands.
// The result maps post id -> reason ("" = valid) and the live posts by id.
// Rows the live decision refuses are marked revoked on the way (so the list
// and the next check agree without recomputing), best-effort.
func (s *Service) evaluateOfflineCopies(ctx context.Context, store offlineStore, viewerID uuid.UUID, deviceID string, rows map[uuid.UUID]postgres.OfflineCopy, now time.Time) (map[uuid.UUID]string, map[uuid.UUID]*postgres.Post, error) {
	reasons := make(map[uuid.UUID]string, len(rows))
	posts := make(map[uuid.UUID]*postgres.Post, len(rows))
	if len(rows) == 0 {
		return reasons, posts, nil
	}
	ids := make([]uuid.UUID, 0, len(rows))
	for id := range rows {
		ids = append(ids, id)
	}
	loaded, err := store.GetPostsByIDs(ctx, ids)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: load posts: %v", ErrOfflineUnavailable, err)
	}
	live := make([]*postgres.Post, 0, len(loaded))
	for i := range loaded {
		p := &loaded[i]
		posts[p.ID] = p
		live = append(live, p)
	}
	judge, err := s.offlineJudgeFor(ctx, viewerID, live)
	if err != nil {
		return nil, nil, err
	}
	revoke := map[string][]uuid.UUID{}
	for id, row := range rows {
		liveReason, err := judge(posts[id]) // a post the store did not return is deleted
		if err != nil {
			return nil, nil, err
		}
		reasons[id] = offlineValidity(row, liveReason, now)
		if row.RevokedAt == nil {
			if r := offlineRevokeReason(liveReason); r != "" {
				revoke[r] = append(revoke[r], id)
			}
		}
	}
	for reason, postIDs := range revoke {
		if _, err := store.RevokeOfflineCopies(ctx, viewerID, deviceID, postIDs, reason, now); err != nil {
			slog.WarnContext(ctx, "offline copy: revoke on check failed", "reason", reason, "posts", len(postIDs), "err", err)
		}
	}
	return reasons, posts, nil
}

// CheckOfflineCopies answers, for each id, whether the caller's copy on
// deviceID is still valid. Ids are answered in the order sent, duplicates
// once; an id that is not a post id, and a post the device holds no copy
// of, are "unknown". A valid copy has last_checked_at stamped; its expiry
// is never moved by a check.
//
// How long ago the device last checked is not part of the answer.
// recheck_after_seconds is advice to the client about when to ask again; a
// device that was offline for a week and then asks is answered exactly like
// one that asked yesterday, and nothing is ever revoked for being overdue.
func (s *Service) CheckOfflineCopies(ctx context.Context, viewerID uuid.UUID, rawDeviceID string, rawIDs []string) ([]OfflineCheckItem, error) {
	store := s.offlineStore()
	if store == nil {
		return nil, ErrOfflineUnavailable
	}
	deviceID, err := NormalizeOfflineDeviceID(rawDeviceID)
	if err != nil {
		return nil, err
	}
	if len(rawIDs) > MaxOfflineCheckIDs {
		return nil, ErrOfflineTooMany
	}
	type asked struct {
		raw string
		id  uuid.UUID
		ok  bool
	}
	var order []asked
	var ids []uuid.UUID
	seen := map[string]bool{}
	for _, raw := range rawIDs {
		raw = strings.TrimSpace(raw)
		id, perr := uuid.Parse(raw)
		key := raw
		if perr == nil {
			key = id.String()
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		order = append(order, asked{raw: key, id: id, ok: perr == nil && id != uuid.Nil})
		if perr == nil && id != uuid.Nil {
			ids = append(ids, id)
		}
	}
	rows, err := store.OfflineCopiesForPosts(ctx, viewerID, deviceID, ids)
	if err != nil {
		return nil, fmt.Errorf("%w: load copies: %v", ErrOfflineUnavailable, err)
	}
	now := s.clock().UTC()
	reasons, posts, err := s.evaluateOfflineCopies(ctx, store, viewerID, deviceID, rows, now)
	if err != nil {
		return nil, err
	}

	var validPosts []*postgres.Post
	for id, reason := range reasons {
		if reason == "" && posts[id] != nil {
			validPosts = append(validPosts, posts[id])
		}
	}
	renewable := s.offlineRenewable(ctx, validPosts)

	out := make([]OfflineCheckItem, 0, len(order))
	var valid []uuid.UUID
	for _, a := range order {
		item := OfflineCheckItem{PostID: a.raw}
		row, has := rows[a.id]
		switch {
		case !a.ok || !has:
			item.Reason = OfflineReasonUnknown
		case reasons[a.id] != "":
			item.Reason = reasons[a.id]
		default:
			item.Valid = true
			exp := row.ExpiresAt.UTC()
			item.ExpiresAt = &exp
			if p := posts[a.id]; p != nil {
				item.ContentType = p.ContentType
			}
			renew := renewable[a.id]
			item.Renewable = &renew
			valid = append(valid, a.id)
		}
		out = append(out, item)
	}
	if err := store.TouchOfflineCopies(ctx, viewerID, deviceID, valid, now); err != nil {
		slog.WarnContext(ctx, "offline copy: last_checked_at not stamped", "copies", len(valid), "err", err)
	}
	return out, nil
}

// offlineRenewable answers, for posts whose copies the check just found
// valid, whether repeating the grant would succeed right now. A valid copy
// has already passed the grant's steps 1, 2, 4 and 5 against the post as it
// stands (the viewer may watch it, the creator allows it or the viewer is
// the creator, and a members-only viewer is entitled), and renewing an
// active copy never counts against the limit (step 8). What is left is
// steps 3 and 6: it is still a long video or a reel with a video on it, it
// is published, and its media is ready and passed (the owner's own
// scheduled or re-processing post is valid but not renewable).
//
// Step 7 — that media-service still lists a 720p, 480p or 360p rendition —
// is NOT asked here: it is one HTTP call per video, and a check carries up
// to 100. A ready, passed video that has lost its whole ladder is the one
// case where `renewable` is true and the grant then answers 409 NOT_READY.
//
// Best-effort: if the media state cannot be read, every answer is false (a
// client skips a renewal and asks again later); the check itself still
// answers, because validity never depended on it.
func (s *Service) offlineRenewable(ctx context.Context, posts []*postgres.Post) map[uuid.UUID]bool {
	out := make(map[uuid.UUID]bool, len(posts))
	if len(posts) == 0 {
		return out
	}
	if err := s.attachMediaState(ctx, posts); err != nil {
		slog.WarnContext(ctx, "offline copy: renewable not computed", "copies", len(posts), "err", err)
		return out
	}
	for _, p := range posts {
		out[p.ID] = offlineContentType(p.ContentType) && p.PublishAt == nil && !p.IsProcessing && primaryVideo(p) != nil
	}
	return out
}

// ListOfflineCopies returns the caller's valid copies on deviceID, newest
// grant first: the grant's card without media.path, so a device that lost
// its own list can rebuild it. A copy the live decision now refuses is left
// out (and marked revoked), exactly as the check would answer it.
func (s *Service) ListOfflineCopies(ctx context.Context, viewerID uuid.UUID, rawDeviceID string) ([]OfflineCard, error) {
	store := s.offlineStore()
	if store == nil {
		return nil, ErrOfflineUnavailable
	}
	deviceID, err := NormalizeOfflineDeviceID(rawDeviceID)
	if err != nil {
		return nil, err
	}
	now := s.clock().UTC()
	active, err := store.ListActiveOfflineCopies(ctx, viewerID, deviceID, now)
	if err != nil {
		return nil, fmt.Errorf("%w: list copies: %v", ErrOfflineUnavailable, err)
	}
	rows := make(map[uuid.UUID]postgres.OfflineCopy, len(active))
	for _, row := range active {
		rows[row.PostID] = row
	}
	reasons, posts, err := s.evaluateOfflineCopies(ctx, store, viewerID, deviceID, rows, now)
	if err != nil {
		return nil, err
	}
	var kept []*postgres.Post
	for _, row := range active {
		if reasons[row.PostID] == "" && posts[row.PostID] != nil {
			kept = append(kept, posts[row.PostID])
		}
	}
	if err := s.attachMediaState(ctx, kept); err != nil {
		return nil, fmt.Errorf("%w: media state: %v", ErrOfflineUnavailable, err)
	}
	names := s.offlineChannelNames(ctx, viewerID, kept)

	out := make([]OfflineCard, 0, len(kept))
	for _, row := range active {
		p := posts[row.PostID]
		if reasons[row.PostID] != "" || p == nil {
			continue
		}
		var stored offlineStoredCard
		if len(row.Card) > 0 {
			if err := json.Unmarshal(row.Card, &stored); err != nil {
				slog.WarnContext(ctx, "offline copy: stored card unreadable", "post_id", row.PostID, "err", err)
			}
		}
		stored.Media.Path = "" // the list never hands out a path
		durationMs := 0
		if video := primaryVideo(p); video != nil {
			durationMs = video.DurationMs
			if stored.Media.MediaID == uuid.Nil {
				stored.Media.MediaID = video.MediaID
			}
		}
		out = append(out, *s.offlineCard(p, row.ExpiresAt, stored, durationMs, names))
	}
	return out, nil
}

// RemoveOfflineCopy marks the caller's copy of postID on deviceID removed.
// Idempotent, and deliberately ungated: a copy of a post that has since
// become private or been deleted can still be removed.
func (s *Service) RemoveOfflineCopy(ctx context.Context, viewerID, postID uuid.UUID, rawDeviceID string) (*OfflineRemoved, error) {
	store := s.offlineStore()
	if store == nil {
		return nil, ErrOfflineUnavailable
	}
	deviceID, err := NormalizeOfflineDeviceID(rawDeviceID)
	if err != nil {
		return nil, err
	}
	if _, err := store.RevokeOfflineCopies(ctx, viewerID, deviceID, []uuid.UUID{postID}, postgres.OfflineRevokeRemoved, s.clock().UTC()); err != nil {
		return nil, fmt.Errorf("%w: remove: %v", ErrOfflineUnavailable, err)
	}
	return &OfflineRemoved{PostID: postID, Removed: true}, nil
}

// OfflineErrorStatus is the error -> (status, code) table of the offline
// routes (http/offline_copies.go writes it).
func OfflineErrorStatus(err error) (int, string) {
	switch {
	case errors.Is(err, ErrPostNotFound), errors.Is(err, ErrPostNotVisible):
		return http.StatusNotFound, "NOT_FOUND"
	case errors.Is(err, ErrOfflineNotAllowed):
		return http.StatusForbidden, "OFFLINE_NOT_ALLOWED"
	case errors.Is(err, ErrOfflineUnsupported):
		return http.StatusUnprocessableEntity, "UNSUPPORTED_CONTENT"
	case errors.Is(err, ErrOfflineDevice):
		return http.StatusUnprocessableEntity, "INVALID_DEVICE"
	case errors.Is(err, ErrOfflineTooMany):
		return http.StatusUnprocessableEntity, "INVALID_REQUEST"
	case errors.Is(err, ErrOfflineNotReady):
		return http.StatusConflict, "NOT_READY"
	case errors.Is(err, ErrOfflineLimit):
		return http.StatusConflict, "OFFLINE_LIMIT"
	case errors.Is(err, ErrOfflineUnavailable), errors.Is(err, ErrStoryPolicyUnresolved):
		return http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE"
	default:
		return http.StatusInternalServerError, "INTERNAL_ERROR"
	}
}

// ── caption tracks from media-service ───────────────────────────────────────

// httpOfflineCaptionSource reads GET {mediaServiceURL}/v1/subtitles/:mediaId
// AS THE VIEWER (X-User-Id beside the internal key), so media-service makes
// its own audience decision and applies its own draft rule: a caption the
// viewer could not turn on in the player is never listed on a card.
type httpOfflineCaptionSource struct{ svc *Service }

func (h httpOfflineCaptionSource) CaptionTracks(ctx context.Context, viewerID, mediaID uuid.UUID) ([]OfflineCaption, error) {
	s := h.svc
	if s == nil || s.mediaServiceURL == "" || s.httpClient == nil {
		return nil, errors.New("media-service not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(s.mediaServiceURL, "/")+"/v1/subtitles/"+mediaID.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-User-Id", viewerID.String())
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return nil, fmt.Errorf("media-service answered %d", resp.StatusCode)
	}
	var envelope struct {
		Data struct {
			Subtitles []struct {
				Language  string `json:"language"`
				Source    string `json:"source"`
				Published *bool  `json:"published"`
			} `json:"subtitles"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxOfflineCaptionBody)).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode subtitles: %w", err)
	}
	out := make([]OfflineCaption, 0, len(envelope.Data.Subtitles))
	seen := map[string]bool{}
	for _, t := range envelope.Data.Subtitles {
		lang := strings.TrimSpace(t.Language)
		if !validLanguageTag(lang) || seen[lang] {
			continue
		}
		// A draft is the creator's alone; it is not part of anyone's copy,
		// the creator's included.
		if t.Published != nil && !*t.Published {
			continue
		}
		seen[lang] = true
		out = append(out, OfflineCaption{
			Lang: lang, Label: offlineCaptionLabel(lang, t.Source),
			Path: "/v1/subtitles/" + mediaID.String() + "/track/" + lang + ".vtt",
		})
	}
	return out, nil
}

// offlineLanguageNames are the display names of the languages the caption
// pipeline produces today; any other code is shown as the code itself.
var offlineLanguageNames = map[string]string{
	"en": "English", "hi": "Hindi", "te": "Telugu", "ta": "Tamil", "kn": "Kannada", "ml": "Malayalam",
	"mr": "Marathi", "bn": "Bengali", "gu": "Gujarati", "pa": "Punjabi", "ur": "Urdu", "or": "Odia",
	"es": "Spanish", "fr": "French", "de": "German", "pt": "Portuguese", "ru": "Russian", "ar": "Arabic",
	"ja": "Japanese", "ko": "Korean", "zh": "Chinese", "id": "Indonesian", "it": "Italian", "tr": "Turkish",
}

// offlineCaptionLabel is what a caption track is called in a player menu.
func offlineCaptionLabel(lang, source string) string {
	name := offlineLanguageNames[strings.ToLower(lang)]
	if name == "" {
		if base, _, found := strings.Cut(lang, "-"); found {
			name = offlineLanguageNames[strings.ToLower(base)]
		}
	}
	if name == "" {
		name = lang
	}
	if source == "auto_generated" {
		return name + " (auto-generated)"
	}
	return name
}

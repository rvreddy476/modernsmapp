package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

/*
	Live -> video (MTube, 2026-09-27).

	live-service-v2 emits live.stream.vod_ready {stream_id, creator_id,
	recording_url, duration_sec} once LiveKit egress has written the
	recording. The consumer (internal/consumers/live_vod.go) hands that to
	CreateLiveVODPost, which makes ONE long_video post for the streamer:

	  title      = the stream's title (payload `title` when present, else
	               read from live-service-v2; else "Live stream, <date>")
	  visibility = unlisted, source = live, live_stream_id = the stream
	  media      = the media_assets row behind the recording, which must be
	               processing_status 'ready' — otherwise the event is
	               skipped (not retried; the media pipeline re-announces)

	The recording is resolved to a media asset in this order: an explicit
	`media_asset_id` in the payload; a media id in the recording URL's path
	(/v1/media/<id>/...); the asset whose storage_key ends with the URL's
	object key. No match = skipped with a log line, because a post with no
	playable asset is worse than no post.

	Idempotent on stream id (uq_posts_live_stream + the store's advisory
	lock). The usual PostCreated outbox row is committed with the post, with
	visibility 'unlisted', so feeds learn about it exactly the way they learn
	about any unlisted upload — and see it once the owner makes it public
	through PATCH /v1/posts/:id (which emits the eligibility change).
*/

// LiveVODInput is what the consumer decodes from the event. MediaAssetID
// and Title are optional extensions of the shared payload.
type LiveVODInput struct {
	StreamID     uuid.UUID
	CreatorID    uuid.UUID
	RecordingURL string
	DurationSec  int
	MediaAssetID *uuid.UUID
	Title        string
}

// LiveVODOutcome says what CreateLiveVODPost did.
type LiveVODOutcome struct {
	Post    *postgres.Post
	Created bool
	// Skipped is non-empty when no post was made and none will be by a
	// retry: the reason, for the log.
	Skipped string
}

// liveVODStore is the slice of the store the flow needs; an interface so
// the decision path is testable without a database.
type liveVODStore interface {
	GetPostByLiveStream(ctx context.Context, streamID uuid.UUID) (*postgres.Post, error)
	BatchGetMediaOwnership(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]postgres.MediaOwnership, error)
	FindMediaByStorageKeySuffix(ctx context.Context, keySuffix string) (uuid.UUID, error)
	CreateLiveVODPost(ctx context.Context, in postgres.LiveVODInsert) (*postgres.Post, bool, error)
}

// SetLiveServiceURL configures live-service-v2's base URL, used to read the
// stream title when the event does not carry one.
func (s *Service) SetLiveServiceURL(u string) { s.liveServiceURL = u }

// CreateLiveVODPost is the whole decision; see the package comment.
func (s *Service) CreateLiveVODPost(ctx context.Context, in LiveVODInput) (*LiveVODOutcome, error) {
	if s.liveVOD == nil {
		return nil, ErrAuthoringStoreUnavailable
	}
	if in.StreamID == uuid.Nil || in.CreatorID == uuid.Nil {
		return &LiveVODOutcome{Skipped: "stream_id and creator_id are required"}, nil
	}
	// Fast idempotency path before any media lookup.
	if existing, err := s.liveVOD.GetPostByLiveStream(ctx, in.StreamID); err != nil {
		return nil, fmt.Errorf("lookup vod post: %w", err)
	} else if existing != nil {
		return &LiveVODOutcome{Post: existing}, nil
	}

	mediaID, err := s.resolveRecordingMedia(ctx, in)
	if err != nil {
		return nil, err
	}
	if mediaID == uuid.Nil {
		return &LiveVODOutcome{Skipped: "recording is not registered as a media asset"}, nil
	}
	ownership, err := s.liveVOD.BatchGetMediaOwnership(ctx, []uuid.UUID{mediaID})
	if err != nil {
		return nil, fmt.Errorf("vod media state: %w", err)
	}
	m, ok := ownership[mediaID]
	if !ok {
		return &LiveVODOutcome{Skipped: "recording media asset not found"}, nil
	}
	if m.ProcessingStatus != mediaReady {
		return &LiveVODOutcome{Skipped: "recording media is " + m.ProcessingStatus + ", not ready"}, nil
	}
	if m.Kind != "video" {
		return &LiveVODOutcome{Skipped: "recording media is not a video"}, nil
	}

	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = s.fetchLiveStreamTitle(ctx, in.StreamID, in.CreatorID)
	}
	if title == "" {
		title = "Live stream, " + time.Now().UTC().Format("2 Jan 2006")
	}
	if len([]rune(title)) > MaxTitleRunes {
		title = string([]rune(title)[:MaxTitleRunes])
	}

	now := time.Now()
	reviewStatus := "approved"
	if !mediaPublishable(m.ProcessingStatus, m.ModerationStatus) {
		// Scanned later: held author-only exactly like an upload whose
		// verdict has not landed; the transcode consumer releases it.
		reviewStatus = "pending"
	}
	post := &postgres.Post{
		ID:                  uuid.New(),
		AuthorID:            in.CreatorID,
		Text:                "",
		Visibility:          "unlisted",
		ContentType:         "long_video",
		ContentTypeExplicit: true,
		PostType:            "video",
		AppOrigin:           "live",
		ShareToPostbook:     false,
		PublishToFeed:       true,
		AllowEmbedding:      true,
		AllowDownload:       true,
		ReviewStatus:        reviewStatus,
		Title:               title,
		CreatedAt:           now,
		UpdatedAt:           now,
		Media:               []postgres.PostMedia{{MediaID: mediaID, Kind: "video", Position: 0}},
	}
	durationSec := in.DurationSec
	if durationSec <= 0 && m.DurationMs > 0 {
		durationSec = m.DurationMs / 1000
	}
	vm := &postgres.VideoMetadata{
		PostID:           post.ID,
		DurationSeconds:  float64(durationSec),
		Orientation:      "landscape",
		ComputedCategory: "long_video",
		FinalCategory:    "long_video",
		UploadStatus:     mediaReady,
		MediaAssetID:     &mediaID,
	}
	insert := postgres.LiveVODInsert{Post: post, StreamID: in.StreamID, MediaID: mediaID, VideoMetadata: vm}
	if s.producer != nil {
		insert.EventType = events.PostCreated
		insert.EventPayload = s.buildPostCreatedPayload(ctx, post, nil, durationSec, 1)
	}
	created, wasCreated, err := s.liveVOD.CreateLiveVODPost(ctx, insert)
	if err != nil {
		return nil, fmt.Errorf("create vod post: %w", err)
	}
	if wasCreated {
		s.NudgeOutbox()
	}
	return &LiveVODOutcome{Post: created, Created: wasCreated}, nil
}

// resolveRecordingMedia maps the event onto a media asset id; uuid.Nil
// when nothing matches.
func (s *Service) resolveRecordingMedia(ctx context.Context, in LiveVODInput) (uuid.UUID, error) {
	if in.MediaAssetID != nil && *in.MediaAssetID != uuid.Nil {
		return *in.MediaAssetID, nil
	}
	if id := MediaIDFromRecordingURL(in.RecordingURL); id != uuid.Nil {
		if ownership, err := s.liveVOD.BatchGetMediaOwnership(ctx, []uuid.UUID{id}); err == nil {
			if _, ok := ownership[id]; ok {
				return id, nil
			}
		}
	}
	key := RecordingObjectKey(in.RecordingURL)
	if key == "" {
		return uuid.Nil, nil
	}
	return s.liveVOD.FindMediaByStorageKeySuffix(ctx, key)
}

// MediaIDFromRecordingURL finds a media id in a gateway-shaped recording
// URL (/v1/media/<id>/...); uuid.Nil otherwise. Pure.
func MediaIDFromRecordingURL(raw string) uuid.UUID {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return uuid.Nil
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, p := range parts {
		if p == "media" && i+1 < len(parts) {
			if id, err := uuid.Parse(parts[i+1]); err == nil {
				return id
			}
		}
	}
	return uuid.Nil
}

// RecordingObjectKey is the storage object key a recording URL points at:
// the URL path with the bucket segment dropped when the URL is an S3-style
// endpoint URL, or the bare key when the payload carried one. Pure.
//
//	https://cdn.example/live-recordings/<id>.mp4      -> live-recordings/<id>.mp4
//	http://minio:9000/bucket/live-recordings/<id>.mp4 -> live-recordings/<id>.mp4 (suffix match covers the bucket)
//	live-recordings/<id>.mp4                          -> live-recordings/<id>.mp4
func RecordingObjectKey(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		return strings.TrimLeft(raw, "/")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	path := strings.Trim(u.Path, "/")
	if path == "" {
		return ""
	}
	// Keep the last two segments at most: "<prefix>/<file>". A LIKE '%'
	// suffix match on storage_key then tolerates any bucket or CDN prefix.
	parts := strings.Split(path, "/")
	if len(parts) > 2 {
		parts = parts[len(parts)-2:]
	}
	return strings.Join(parts, "/")
}

// fetchLiveStreamTitle asks live-service-v2 for the stream (as its owner,
// through the internal key) and returns its title. Best-effort: "" on any
// failure.
func (s *Service) fetchLiveStreamTitle(ctx context.Context, streamID, creatorID uuid.UUID) string {
	if s.liveServiceURL == "" || s.httpClient == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(s.liveServiceURL, "/")+"/v1/livestream/streams/"+streamID.String(), nil)
	if err != nil {
		return ""
	}
	req.Header.Set("X-User-Id", creatorID.String())
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		slog.WarnContext(ctx, "live vod: stream title lookup skipped", "stream_id", streamID, "err", err)
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var envelope struct {
		Data struct {
			Title string `json:"title"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&envelope); err != nil {
		return ""
	}
	return strings.TrimSpace(envelope.Data.Title)
}

// ErrLiveVODSkipped is returned by the consumer wrapper for a skipped
// event so the log line names the reason; never retried.
var ErrLiveVODSkipped = errors.New("live vod skipped")

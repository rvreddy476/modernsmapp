package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// MTube captions list for creators (2026-09-27).
//
//	GET   /v1/subtitles/mine?status=all|draft|published&cursor=&limit=
//	PATCH /v1/subtitles/:mediaId/:language {published}
//
// State only — the transcript stays behind the gated caption reads.

// ErrInvalidCaptionFilter is a status value outside all|draft|published.
var ErrInvalidCaptionFilter = errors.New("status must be all, draft or published")

// ErrInvalidCaptionCursor is a cursor this service did not issue.
var ErrInvalidCaptionCursor = errors.New("invalid cursor")

// CaptionMediaItem is one media asset's caption tracks in the creator list.
type CaptionMediaItem struct {
	MediaID uuid.UUID `json:"media_id"`
	// PostID is always null here: media-service does not know which post
	// (if any) references an asset. The client resolves it, if it needs
	// to, through post-service's own uploads list.
	PostID     *uuid.UUID                     `json:"post_id"`
	Languages  []postgres.SubtitleLanguageRow `json:"languages"`
	ModifiedAt time.Time                      `json:"modified_at"`
}

// CaptionListPage is one page of the creator list.
type CaptionListPage struct {
	Items      []CaptionMediaItem
	NextCursor string
}

// MaxCaptionListLimit bounds one page.
const MaxCaptionListLimit = 100

func parseCaptionStatus(raw string) (postgres.SubtitleStatusFilter, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "all":
		return postgres.SubtitleStatusAll, nil
	case "draft":
		return postgres.SubtitleStatusDraft, nil
	case "published":
		return postgres.SubtitleStatusPublished, nil
	}
	return "", ErrInvalidCaptionFilter
}

// encodeCaptionCursor / decodeCaptionCursor carry the keyset position as an
// opaque token: base64url of "<RFC3339Nano>|<media uuid>".
func encodeCaptionCursor(c postgres.SubtitleListCursor) string {
	raw := c.ModifiedAt.UTC().Format(time.RFC3339Nano) + "|" + c.MediaID.String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCaptionCursor(token string) (postgres.SubtitleListCursor, error) {
	if strings.TrimSpace(token) == "" {
		return postgres.SubtitleListCursor{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return postgres.SubtitleListCursor{}, ErrInvalidCaptionCursor
	}
	at, id, ok := strings.Cut(string(raw), "|")
	if !ok {
		return postgres.SubtitleListCursor{}, ErrInvalidCaptionCursor
	}
	ts, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return postgres.SubtitleListCursor{}, ErrInvalidCaptionCursor
	}
	mediaID, err := uuid.Parse(id)
	if err != nil {
		return postgres.SubtitleListCursor{}, ErrInvalidCaptionCursor
	}
	return postgres.SubtitleListCursor{ModifiedAt: ts, MediaID: mediaID}, nil
}

// ListMyCaptions lists the caller's caption tracks grouped per media asset.
func (s *Service) ListMyCaptions(ctx context.Context, callerID uuid.UUID, status, cursor string, limit int) (*CaptionListPage, error) {
	filter, err := parseCaptionStatus(status)
	if err != nil {
		return nil, err
	}
	pos, err := decodeCaptionCursor(cursor)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > MaxCaptionListLimit {
		limit = MaxCaptionListLimit
	}
	groups, err := s.pgStore.ListSubtitlesByUploader(ctx, callerID, filter, pos, limit)
	if err != nil {
		return nil, fmt.Errorf("list captions: %w", err)
	}
	page := &CaptionListPage{Items: make([]CaptionMediaItem, 0, len(groups))}
	if len(groups) > limit {
		groups = groups[:limit]
		last := groups[len(groups)-1]
		page.NextCursor = encodeCaptionCursor(postgres.SubtitleListCursor{ModifiedAt: last.ModifiedAt, MediaID: last.MediaID})
	}
	for _, g := range groups {
		langs := g.Languages
		if langs == nil {
			langs = []postgres.SubtitleLanguageRow{}
		}
		page.Items = append(page.Items, CaptionMediaItem{MediaID: g.MediaID, Languages: langs, ModifiedAt: g.ModifiedAt})
	}
	return page, nil
}

// SetCaptionPublished publishes or unpublishes one of the caller's tracks.
// ErrNotMediaOwner when the asset is not theirs (or does not exist — the
// same answer, so the route does not enumerate assets);
// ErrCaptionTrackNotFound when the asset is theirs but has no such track.
func (s *Service) SetCaptionPublished(ctx context.Context, callerID, mediaID uuid.UUID, language string, published bool) (*postgres.MediaSubtitle, error) {
	language = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(language, ".vtt")))
	if language == "" || len(language) > 10 {
		return nil, fmt.Errorf("%w: language is required", ErrInvalidCaption)
	}
	if err := s.AssertMediaOwner(ctx, mediaID, callerID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No such asset is answered like not-yours: the route must not
			// enumerate assets.
			return nil, ErrNotMediaOwner
		}
		return nil, err
	}
	sub, err := s.pgStore.SetSubtitlePublished(ctx, mediaID, language, published)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCaptionTrackNotFound
		}
		return nil, fmt.Errorf("set caption published: %w", err)
	}
	return sub, nil
}

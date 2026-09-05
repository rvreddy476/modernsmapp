package http

import (
	"context"
	"encoding/json"

	"github.com/atpost/search-service/internal/store/search"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Result rows for page-scoped search (Android, 2026-09-05).
//
// The index stores ids, not URLs (signed rendition URLs are short-lived),
// so a result page is hydrated here: authors from users_v1 in one query,
// thumbnails and avatars from media-service in one batch. Both steps are
// best-effort — a row is never dropped because its author or media could
// not be resolved; the field is null instead.

// AuthorRef is the card-sized author on a post result row.
type AuthorRef struct {
	ID          string  `json:"id"`
	Username    string  `json:"username"`
	DisplayName string  `json:"display_name"`
	AvatarURL   *string `json:"avatar_url"`
}

// PostResult is one post row. It is the indexed document plus:
//
//	id            — alias of post_id
//	author        — {id, username, display_name, avatar_url}
//	thumbnail_url — first attached asset's image rendition (poster frame
//	                for a video), null when there is none
//	playback_url  — for a video: the URL a player opens (null otherwise)
//
// title, content_type, duration_ms, media_id, media_kind, text, hashtags,
// like_count, comment_count and created_at come from the document.
type PostResult struct {
	search.PostDoc
	ID           string     `json:"id"`
	Author       *AuthorRef `json:"author"`
	ThumbnailURL *string    `json:"thumbnail_url"`
	PlaybackURL  *string    `json:"playback_url"`
}

// UserResult is one user row: the indexed document plus avatar_url.
type UserResult struct {
	search.UserDoc
	AvatarURL *string `json:"avatar_url"`
}

// viewerFrom reads the gateway-stamped X-User-Id; uuid.Nil when absent.
func viewerFrom(c *gin.Context) uuid.UUID {
	id, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		return uuid.Nil
	}
	return id
}

func viewerString(viewerID uuid.UUID) string {
	if viewerID == uuid.Nil {
		return ""
	}
	return viewerID.String()
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// postResults hydrates a page of post documents into result rows.
func (h *Handler) postResults(ctx context.Context, viewerID uuid.UUID, docs []search.PostDoc) []PostResult {
	rows := make([]PostResult, 0, len(docs))
	if len(docs) == 0 {
		return rows
	}

	authorIDs := make([]string, 0, len(docs))
	seenAuthor := make(map[string]bool, len(docs))
	mediaIDs := make([]string, 0, len(docs)*2)
	for _, d := range docs {
		if d.AuthorID != "" && !seenAuthor[d.AuthorID] {
			seenAuthor[d.AuthorID] = true
			authorIDs = append(authorIDs, d.AuthorID)
		}
		if d.MediaID != "" {
			mediaIDs = append(mediaIDs, d.MediaID)
		}
	}

	authors := map[string]search.UserDoc{}
	if h.store != nil {
		if got, err := h.store.GetUsersByIDs(ctx, authorIDs); err == nil {
			authors = got
		}
	}
	for _, a := range authors {
		if a.AvatarMediaID != "" {
			mediaIDs = append(mediaIDs, a.AvatarMediaID)
		}
	}
	assets := h.mediaClient.Resolve(ctx, viewerString(viewerID), mediaIDs)

	for _, d := range docs {
		row := PostResult{PostDoc: d, ID: d.PostID}
		author := &AuthorRef{ID: d.AuthorID, Username: d.AuthorUsername}
		if a, ok := authors[d.AuthorID]; ok {
			author.Username = a.Username
			author.DisplayName = a.DisplayName
			if a.AvatarMediaID != "" {
				if asset, ok := assets[a.AvatarMediaID]; ok {
					author.AvatarURL = strPtr(asset.ThumbnailURL())
				}
			}
		}
		row.Author = author
		if d.MediaID != "" {
			if asset, ok := assets[d.MediaID]; ok {
				row.ThumbnailURL = strPtr(asset.ThumbnailURL())
				if asset.Kind == "video" {
					row.PlaybackURL = strPtr(asset.PlaybackURL)
					// A document indexed before the transcode measured the
					// clip carries no duration; media-service knows it now.
					if row.DurationMs == 0 {
						row.DurationMs = asset.DurationMs
					}
				}
				if row.MediaKind == "" {
					row.MediaKind = asset.Kind
				}
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// userResults hydrates a page of user documents with avatar URLs.
func (h *Handler) userResults(ctx context.Context, viewerID uuid.UUID, docs []search.UserDoc) []UserResult {
	rows := make([]UserResult, 0, len(docs))
	if len(docs) == 0 {
		return rows
	}
	mediaIDs := make([]string, 0, len(docs))
	for _, d := range docs {
		if d.AvatarMediaID != "" {
			mediaIDs = append(mediaIDs, d.AvatarMediaID)
		}
	}
	assets := h.mediaClient.Resolve(ctx, viewerString(viewerID), mediaIDs)
	for _, d := range docs {
		row := UserResult{UserDoc: d}
		if d.AvatarMediaID != "" {
			if asset, ok := assets[d.AvatarMediaID]; ok {
				row.AvatarURL = strPtr(asset.ThumbnailURL())
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// rankedPostItems hydrates the multi-entity `posts` items (raw _source
// maps) into the same row shape, returned as maps so the ranked response
// keeps its type.
func (h *Handler) rankedPostItems(ctx context.Context, viewerID uuid.UUID, items []map[string]any) []map[string]any {
	if len(items) == 0 {
		return items
	}
	docs := make([]search.PostDoc, 0, len(items))
	for _, it := range items {
		raw, err := json.Marshal(it)
		if err != nil {
			continue
		}
		var d search.PostDoc
		if err := json.Unmarshal(raw, &d); err != nil {
			continue
		}
		docs = append(docs, d)
	}
	rows := h.postResults(ctx, viewerID, docs)
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		raw, err := json.Marshal(r)
		if err != nil {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		out = append(out, m)
	}
	return out
}

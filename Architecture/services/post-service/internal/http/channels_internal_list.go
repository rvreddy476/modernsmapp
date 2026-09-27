package http

import (
	"net/http"
	"strings"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Tube catalogue listing for search (MTube, 2026-09-27).
//
//	GET /internal/tube/channels?after=<uuid>&limit=<n>
//	GET /internal/tube/playlists?after=<uuid>&limit=<n>
//
// search-service walks these to build and heal its tube_channels_v1 /
// tube_collections_v1 indices (search-service internal/reindex/tube.go).
// Channels and playlists publish no lifecycle events, so a walk over the
// source of truth is the only feed the index has.
//
// Shape follows the other internal keyset walks in this service
// (ListSubscriberIDsInternal): {items, next_after, has_more}, `after` a uuid
// cursor over the surrogate id, `limit` default 200 capped at 500. Internal
// key gated like every route on this engine; the gateway blocks /internal/.
//
// A NON-PUBLIC PLAYLIST DISCLOSES ONLY ITS ID AND VISIBILITY. The reader
// needs to know the row exists (so it can delete a document for a playlist
// that went private) and nothing else — not the title, not the owner. An
// internal key is a weaker fence than "the data was never sent".

const (
	tubeCatalogueDefaultLimit = 200
	tubeCatalogueMaxLimit     = 500
)

// registerTubeCatalogueInternalRoutes is called from RegisterRoutes.
func (h *Handler) registerTubeCatalogueInternalRoutes(r *gin.Engine) {
	r.GET("/internal/tube/channels", h.ListTubeChannelsInternal)
	r.GET("/internal/tube/playlists", h.ListTubePlaylistsInternal)
}

// tubeChannelListRow is one channel as the search reindex reads it.
type tubeChannelListRow struct {
	ID            uuid.UUID  `json:"id"`
	OwnerID       uuid.UUID  `json:"owner_id"`
	Name          string     `json:"name"`
	Handle        string     `json:"handle"`
	About         string     `json:"about"`
	AvatarMediaID *uuid.UUID `json:"avatar_media_id"`
	FollowerCount int        `json:"follower_count"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// tubePlaylistListRow is one playlist as the search reindex reads it. For
// a non-public playlist only ID and Visibility are set.
type tubePlaylistListRow struct {
	ID          uuid.UUID  `json:"id"`
	OwnerID     *uuid.UUID `json:"owner_id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Visibility  string     `json:"visibility"`
	ItemCount   int        `json:"item_count"`
	// CoverMediaID is CoverURL parsed as a media id when the stored cover
	// is a bare id (the composer stores one); null otherwise. CoverURL is
	// the stored value verbatim.
	CoverMediaID *uuid.UUID `json:"cover_media_id"`
	CoverURL     *string    `json:"cover_url"`
	CreatedAt    *time.Time `json:"created_at"`
	UpdatedAt    *time.Time `json:"updated_at"`
}

type tubeCatalogueListPage[T any] struct {
	Items     []T    `json:"items"`
	NextAfter string `json:"next_after"`
	HasMore   bool   `json:"has_more"`
}

// tubeCatalogueListParams reads ?after (uuid keyset cursor, uuid.Nil when
// absent) and ?limit. ok=false means a 400 was written.
func tubeCatalogueListParams(c *gin.Context) (after uuid.UUID, limit int, ok bool) {
	after, ok = parseAfterCursor(c)
	if !ok {
		return uuid.Nil, 0, false
	}
	limit = pageLimit(c.Query("limit"), tubeCatalogueDefaultLimit, tubeCatalogueMaxLimit)
	return after, limit, true
}

// ListTubeChannelsInternal handles GET /internal/tube/channels.
func (h *Handler) ListTubeChannelsInternal(c *gin.Context) {
	after, limit, ok := tubeCatalogueListParams(c)
	if !ok {
		return
	}
	if h.svc == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable, "UNAVAILABLE", "catalogue not configured", nil)
		return
	}
	channels, err := h.svc.ListChannelsAfter(c.Request.Context(), after, limit)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	page := tubeCatalogueListPage[tubeChannelListRow]{Items: make([]tubeChannelListRow, 0, len(channels))}
	for _, ch := range channels {
		page.Items = append(page.Items, tubeChannelListRow{
			ID: ch.ID, OwnerID: ch.UserID, Name: ch.Name, Handle: ch.Handle, About: ch.About,
			AvatarMediaID: ch.AvatarMediaID, FollowerCount: ch.SubscriberCount,
			CreatedAt: ch.CreatedAt, UpdatedAt: ch.UpdatedAt,
		})
	}
	if len(channels) == limit {
		page.NextAfter = channels[len(channels)-1].ID.String()
		page.HasMore = true
	}
	api.JSON(c.Writer, http.StatusOK, page, nil)
}

// ListTubePlaylistsInternal handles GET /internal/tube/playlists.
func (h *Handler) ListTubePlaylistsInternal(c *gin.Context) {
	after, limit, ok := tubeCatalogueListParams(c)
	if !ok {
		return
	}
	if h.svc == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable, "UNAVAILABLE", "catalogue not configured", nil)
		return
	}
	playlists, err := h.svc.ListPlaylistsAfter(c.Request.Context(), after, limit)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	page := tubeCatalogueListPage[tubePlaylistListRow]{Items: make([]tubePlaylistListRow, 0, len(playlists))}
	for i := range playlists {
		page.Items = append(page.Items, tubePlaylistListRowOf(&playlists[i]))
	}
	if len(playlists) == limit {
		page.NextAfter = playlists[len(playlists)-1].ID.String()
		page.HasMore = true
	}
	api.JSON(c.Writer, http.StatusOK, page, nil)
}

// tubePlaylistListRowOf projects one playlist row, disclosing a non-public
// one by id and visibility only. Pure, for the test.
func tubePlaylistListRowOf(p *postgres.Playlist) tubePlaylistListRow {
	row := tubePlaylistListRow{ID: p.ID, Visibility: p.Visibility}
	if p.Visibility != "public" {
		return row
	}
	owner := p.CreatorID
	created, updated := p.CreatedAt, p.UpdatedAt
	row.OwnerID = &owner
	row.Title = p.Title
	row.Description = p.Description
	row.ItemCount = p.ItemCount
	row.CoverURL = p.CoverURL
	row.CreatedAt = &created
	row.UpdatedAt = &updated
	if p.CoverURL != nil {
		if id, err := uuid.Parse(strings.TrimSpace(*p.CoverURL)); err == nil && id != uuid.Nil {
			row.CoverMediaID = &id
		}
	}
	return row
}

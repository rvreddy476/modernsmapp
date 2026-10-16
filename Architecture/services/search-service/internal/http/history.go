package http

// Search history and popular searches (T-15, 2026-10-14).
//
// Recording. A signed-in search is remembered in the caller's history,
// best-effort and off the request path:
//
//   - GET /v1/search and GET /v1/search/posts record by default — they are
//     the search page's own entry points (web Top/Videos/Reels, Android
//     SearchScreen).
//   - /users, /hashtags, /channels, /collections and /events record only
//     when the client sends ?record=1. Those routes also serve typeahead
//     pickers (@mentions, group invites, composer hashtags, the Studio
//     end-screen channel picker), whose keystrokes are not searches.
//   - ?record=0 opts a default route out.
//   - Never recorded: anonymous callers, continuation pages (?cursor=,
//     ?cursor.<type>=), message search (private) and product search (the
//     shop keeps its own surface).
//
// Bounded: at most historyMaxInFlight writes run at once — a write that
// cannot get a slot is dropped, never queued — and each has
// historyWriteTimeout. A failed write is logged and forgotten; the search
// response has already been decided.

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/atpost/search-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	historyMaxInFlight  = 64
	historyWriteTimeout = 2 * time.Second

	popularWindow       = 7 * 24 * time.Hour
	popularDefaultLimit = 10
	popularMaxLimit     = 20
	popularCacheTTL     = 5 * time.Minute
	// popularDefaultMinUsers distinct people must have searched a query in
	// the window before anyone else is shown it. WithPopularMinUsers may
	// change it, never below postgres.PopularMinUsersFloor.
	popularDefaultMinUsers = 5
)

// searchHistoryStore is the slice of the Postgres extras store the history
// and popular routes use (an interface so the handler tests can fake it).
type searchHistoryStore interface {
	RecordSearchHistory(ctx context.Context, userID uuid.UUID, query string) error
	DeleteSearchHistoryItem(ctx context.Context, id, userID uuid.UUID) error
	PopularQueries(ctx context.Context, window time.Duration, minUsers, limit int) ([]postgres.PopularQuery, error)
}

// historyRecorder owns the bounded, best-effort write path.
type historyRecorder struct {
	once sync.Once
	sem  chan struct{}
	wg   sync.WaitGroup
}

func (r *historyRecorder) slots() chan struct{} {
	r.once.Do(func() { r.sem = make(chan struct{}, historyMaxInFlight) })
	return r.sem
}

// popularCache holds the last popular-searches answer for popularCacheTTL.
type popularCache struct {
	mu    sync.Mutex
	at    time.Time
	items []postgres.PopularQuery
}

// WithPopularMinUsers sets the distinct-user threshold for popular
// searches. Values below postgres.PopularMinUsersFloor are ignored.
func (h *Handler) WithPopularMinUsers(n int) *Handler {
	if n >= postgres.PopularMinUsersFloor {
		h.popularMinUsers = n
	}
	return h
}

// shouldRecord decides whether this request's query goes into history.
// defaultOn is true for the search page's own routes.
func shouldRecord(c *gin.Context, defaultOn bool) bool {
	switch strings.ToLower(strings.TrimSpace(c.Query("record"))) {
	case "1", "true", "yes":
		defaultOn = true
	case "0", "false", "no":
		return false
	}
	if !defaultOn {
		return false
	}
	// A continuation page is the same search, already recorded.
	for k, v := range c.Request.URL.Query() {
		if (k == "cursor" || strings.HasPrefix(k, "cursor.")) && len(v) > 0 && strings.TrimSpace(v[0]) != "" {
			return false
		}
	}
	return true
}

// recordHistory records query for the signed-in caller, best-effort.
// Call it only after the search itself succeeded.
func (h *Handler) recordHistory(c *gin.Context, query string, defaultOn bool) {
	if h.history == nil || !shouldRecord(c, defaultOn) {
		return
	}
	viewer := viewerFrom(c)
	if viewer == uuid.Nil {
		return // anonymous: never recorded
	}
	if _, _, ok := postgres.NormaliseHistoryQuery(query); !ok {
		return
	}
	select {
	case h.recorder.slots() <- struct{}{}:
	default:
		slog.Debug("search history: write dropped, too many in flight")
		return
	}
	h.recorder.wg.Add(1)
	go func() {
		defer h.recorder.wg.Done()
		defer func() { <-h.recorder.slots() }()
		ctx, cancel := context.WithTimeout(context.Background(), historyWriteTimeout)
		defer cancel()
		if err := h.history.RecordSearchHistory(ctx, viewer, query); err != nil {
			slog.Warn("search history: record failed", "error", err)
		}
	}()
}

// WaitHistoryWrites blocks until in-flight history writes finish. Used on
// shutdown and by tests.
func (h *Handler) WaitHistoryWrites() { h.recorder.wg.Wait() }

// DeleteSearchHistoryItem handles DELETE /v1/search/history/:id. Removes
// one entry owned by the caller; an entry that does not exist and one that
// belongs to someone else both answer 404.
func (h *Handler) DeleteSearchHistoryItem(c *gin.Context) {
	if h.history == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "Postgres extras store not configured", nil)
		return
	}
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid search history ID", nil)
		return
	}
	if err := h.history.DeleteSearchHistoryItem(c.Request.Context(), id, userID); err != nil {
		if err == postgres.ErrNotFound {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Search history entry not found", nil)
			return
		}
		slog.Error("DeleteSearchHistoryItem error", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to delete search history entry", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"status": "deleted"}, nil)
}

// PopularSearches handles GET /v1/search/popular?limit= (default 10, max
// 20) — the search page's empty state. Signed-out callers allowed.
//
// Aggregated over the last 7 days of search history; a query appears only
// when at least popularMinUsers distinct people searched it, and never when
// it looks like an email, phone or PAN. Rows are {"query": "..."} — the
// normalised (lower-cased) query, no counts. Cached for 5 minutes. With no
// Postgres, or on a read error with nothing cached, the answer is an empty
// list: an empty state should never be an error page.
func (h *Handler) PopularSearches(c *gin.Context) {
	limit := popularDefaultLimit
	if l := c.Query("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= popularMaxLimit {
			limit = v
		}
	}
	items := h.popularQueries(c.Request.Context())
	if len(items) > limit {
		items = items[:limit]
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": items}, nil)
}

func (h *Handler) popularQueries(ctx context.Context) []postgres.PopularQuery {
	empty := []postgres.PopularQuery{}
	if h.history == nil {
		return empty
	}
	h.popular.mu.Lock()
	defer h.popular.mu.Unlock()
	if h.popular.items != nil && time.Since(h.popular.at) < popularCacheTTL {
		return h.popular.items
	}
	minUsers := popularDefaultMinUsers
	if h.popularMinUsers >= postgres.PopularMinUsersFloor {
		minUsers = h.popularMinUsers
	}
	rows, err := h.history.PopularQueries(ctx, popularWindow, minUsers, popularMaxLimit)
	if err != nil {
		slog.Warn("popular searches: read failed", "error", err)
		if h.popular.items != nil {
			return h.popular.items // stale beats empty
		}
		return empty
	}
	if rows == nil {
		rows = empty
	}
	h.popular.items = rows
	h.popular.at = time.Now()
	return rows
}

package service

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// Test stand-in for graph-service's internal keyset id lists
// (GET /v1/internal/graph/<kind>/<id>?limit=&cursor=), answering in the
// pinned contract (testdata/contracts/graph_internal_lists): a page of ids
// and next_cursor, "" on the last page. Every graph stub in this package
// serves its lists through writeGraphIDPage so they cannot drift apart.

// graphListKind reports which list a request path names: "followers",
// "following" or "connections", or "" for any other path.
func graphListKind(path string) string {
	rest, ok := strings.CutPrefix(path, "/v1/internal/graph/")
	if !ok {
		return ""
	}
	kind, id, ok := strings.Cut(rest, "/")
	if !ok || id == "" || strings.Contains(id, "/") {
		return ""
	}
	switch kind {
	case "followers", "following", "connections":
		return kind
	}
	return ""
}

// graphListOwner is the user id a list path names.
func graphListOwner(path string) uuid.UUID {
	id, _ := uuid.Parse(path[strings.LastIndex(path, "/")+1:])
	return id
}

// stubGraphCursor is the cursor naming `id` as the last row of a page. It
// has graph-service's shape ("<micros>:<uuid>"); the stub only reads the id.
func stubGraphCursor(id uuid.UUID) string { return "1794821400000000:" + id.String() }

// writeGraphIDPage answers one list request from `list` (already in serving
// order): the page strictly after the cursor's id. A cursor that names no
// row of the list, or is not a cursor at all (an offset, say), is a 400
// INVALID_CURSOR, as on graph-service.
func writeGraphIDPage(w http.ResponseWriter, r *http.Request, list []uuid.UUID) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	start := 0
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		_, rawID, _ := strings.Cut(cursor, ":")
		after, err := uuid.Parse(rawID)
		start = -1
		if err == nil {
			for i, id := range list {
				if id == after {
					start = i + 1
					break
				}
			}
		}
		if start < 0 {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "INVALID_CURSOR"}})
			return
		}
	}
	end := min(start+limit, len(list))
	page := make([]string, 0, end-start)
	for _, id := range list[start:end] {
		page = append(page, id.String())
	}
	next := ""
	if end < len(list) {
		next = stubGraphCursor(list[end-1])
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"ids": page, "next_cursor": next}})
}

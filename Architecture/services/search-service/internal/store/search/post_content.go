package search

import (
	"context"
	"log/slog"
	"strings"

	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
)

// Creator metadata on posts_v1 (2026-11-17, long-video finding lv-29 /
// LV-A-5). A post's tags, category and language are written by the
// projection like every other field (the document is replaced whole at
// its search_rev), so an edit that re-emits PostSearchEligibilityChanged
// re-indexes them together with the new title and text. tags is matched
// by the post searches (postTextFields); category and language are
// keyword fields for filters.

// putPostContentMapping adds the three fields to an existing posts index.
// Additive and idempotent, like putCreditsMapping.
func (s *Store) putPostContentMapping(ctx context.Context, index string) {
	req := opensearchapi.IndicesPutMappingRequest{
		Index: []string{index},
		Body:  strings.NewReader(postContentMappingJSON),
	}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		slog.Warn("opensearch: put post content mapping failed", "index", index, "err", err)
		return
	}
	defer res.Body.Close()
	if res.IsError() {
		slog.Warn("opensearch: put post content mapping rejected", "index", index, "status", res.StatusCode)
	}
}

// postContentMappingJSON is the put-mapping body.
const postContentMappingJSON = `{"properties":{
	"tags":     {"type":"text","fields":{"keyword":{"type":"keyword","ignore_above":256}}},
	"category": {"type":"keyword"},
	"language": {"type":"keyword"}
}}`

package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
)

// has_subtitles from media-service (2026-09-27).
//
// The flag is a media-level fact on a post document: media-service announces
// the published-caption state of an asset (MediaSubtitlesChanged) and the
// consumer sets it on every post that attaches the asset. This is the write.
//
// It is a scripted partial update, like IncrementPostViewCount, never a
// projection: the document's body, revision and eligibility belong to the
// projection path, and this touches exactly two fields.
//
//   - A snapshot older than the one already applied is dropped
//     (subtitles_version is the event's OccurredAt in microseconds; the topic
//     is not partitioned by asset, so two snapshots can arrive swapped).
//   - A removed document (tombstone) is left alone: it is not searchable, and
//     its minimal shape is the projection's to define.
//   - A missing document is not an error: a post that is not publicly
//     searchable is not search's business, exactly as for view counts.
//   - Re-applying the same snapshot is a no-op.
//
// A later re-projection of the post (edit, eligibility change) replaces the
// whole document from post-service, which does not know caption state, so
// the flag resets until the next caption change or posts reindex — the same
// trade view_count and engagement_score make (see PostDoc).
const setPostSubtitlesScript = `
boolean removed = ctx._source.containsKey('removed') && ctx._source.removed == true;
long stored = -1;
if (ctx._source.containsKey('subtitles_version') && ctx._source.subtitles_version != null) {
  stored = ((Number)ctx._source.subtitles_version).longValue();
}
if (removed || params.version < stored) {
  ctx.op = 'none';
} else if (params.version == stored && ctx._source.containsKey('has_subtitles') && ctx._source.has_subtitles == params.has) {
  ctx.op = 'none';
} else {
  ctx._source.has_subtitles = params.has;
  ctx._source.subtitles_version = params.version;
}
`

// SetPostHasSubtitles applies one caption-state snapshot to a post document.
// version orders snapshots for the same asset (larger is newer).
func (s *Store) SetPostHasSubtitles(ctx context.Context, postID string, has bool, version int64) error {
	if postID == "" {
		return nil
	}
	body := map[string]any{
		"script": map[string]any{
			"lang":   "painless",
			"source": setPostSubtitlesScript,
			"params": map[string]any{"has": has, "version": version},
		},
	}
	data, _ := json.Marshal(body)
	req := opensearchapi.UpdateRequest{
		Index:      IndexPosts,
		DocumentID: postID,
		Body:       bytes.NewReader(data),
	}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() && res.StatusCode != 404 {
		return fmt.Errorf("update has_subtitles on %s/%s: %s", IndexPosts, postID, res.String())
	}
	return nil
}

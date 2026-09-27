package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
)

// MTube search (2026-09-27).
//
// Three things live here:
//
//  1. The video filters on GET /v1/search/posts?type=videos — sort,
//     duration, date, features — and the four fields they need on the post
//     document: height, has_subtitles, view_count, published_at. The
//     mapping is added to posts_v1 on boot (additive put-mapping, like
//     every field before it); the VALUES arrive on new documents from the
//     event payloads and on existing documents from the next reindex
//     (cmd/backfill -entity posts, which rewrites every eligible row).
//
//  2. tube_channels_v1 — Tube channels (post-service `channels`: name,
//     handle, about), NOT the broadcast channels_v1 index the multi-entity
//     search reads. Fed by the tube reindex (internal/reindex/tube.go):
//     post-service publishes no channel events, so the walk over its
//     internal listing is the only feed.
//
//  3. tube_collections_v1 — public playlists. Same feed. The visibility
//     filter is applied at index time (a non-public playlist is DELETED
//     by the reindex, never written) AND at query time (`term visibility:
//     public`), because the two fail differently: the index-time rule can
//     be a step behind the source, the query-time rule cannot.

const (
	IndexTubeChannels    = "tube_channels_v1"
	IndexTubeCollections = "tube_collections_v1"
)

// TubeChannelDoc is one Tube channel in tube_channels_v1.
type TubeChannelDoc struct {
	ChannelID     string    `json:"channel_id"`
	OwnerID       string    `json:"owner_id"`
	Name          string    `json:"name"`
	Handle        string    `json:"handle"`
	About         string    `json:"about,omitempty"`
	AvatarMediaID string    `json:"avatar_media_id,omitempty"`
	FollowerCount int       `json:"follower_count"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// TubeCollectionDoc is one public playlist in tube_collections_v1.
type TubeCollectionDoc struct {
	PlaylistID   string    `json:"playlist_id"`
	OwnerID      string    `json:"owner_id"`
	Title        string    `json:"title"`
	Description  string    `json:"description,omitempty"`
	Visibility   string    `json:"visibility"`
	ItemCount    int       `json:"item_count"`
	CoverMediaID string    `json:"cover_media_id,omitempty"`
	CoverURL     string    `json:"cover_url,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// initTubeIndices creates the two tube indices when absent and layers the
// video-filter fields onto posts_v1. Called from initIndices on every boot;
// every step is idempotent.
func (s *Store) initTubeIndices(ctx context.Context) {
	settings := opensearchSettingsJSON()
	s.putVideoFilterMapping(ctx, IndexPosts)

	s.createIndexIfNotExists(ctx, IndexTubeChannels, `{
		"settings": `+settings+`,
		"mappings": {
			"properties": {
				"channel_id":      { "type": "keyword" },
				"owner_id":        { "type": "keyword" },
				"name":            { "type": "text", "fields": { "keyword": { "type": "keyword", "ignore_above": 256 } } },
				"handle":          { "type": "text", "analyzer": "keyword", "fields": { "prefix": { "type": "text", "analyzer": "standard" } } },
				"about":           { "type": "text" },
				"avatar_media_id": { "type": "keyword" },
				"follower_count":  { "type": "long" },
				"created_at":      { "type": "date" },
				"updated_at":      { "type": "date" }
			}
		}
	}`)

	s.createIndexIfNotExists(ctx, IndexTubeCollections, `{
		"settings": `+settings+`,
		"mappings": {
			"properties": {
				"playlist_id":    { "type": "keyword" },
				"owner_id":       { "type": "keyword" },
				"title":          { "type": "text", "fields": { "keyword": { "type": "keyword", "ignore_above": 256 } } },
				"description":    { "type": "text" },
				"visibility":     { "type": "keyword" },
				"item_count":     { "type": "long" },
				"cover_media_id": { "type": "keyword" },
				"cover_url":      { "type": "keyword", "index": false },
				"created_at":     { "type": "date" },
				"updated_at":     { "type": "date" }
			}
		}
	}`)
}

// putVideoFilterMapping adds the four video-filter fields to an existing
// posts index. Additive and idempotent, like putResultRowMapping.
func (s *Store) putVideoFilterMapping(ctx context.Context, index string) {
	body := `{"properties":{
		"height":        {"type":"integer"},
		"has_subtitles": {"type":"boolean"},
		"view_count":    {"type":"long"},
		"published_at":  {"type":"date"}
	}}`
	req := opensearchapi.IndicesPutMappingRequest{
		Index: []string{index},
		Body:  strings.NewReader(body),
	}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		slog.Warn("opensearch: put video-filter mapping failed", "index", index, "err", err)
		return
	}
	defer res.Body.Close()
	if res.IsError() {
		slog.Warn("opensearch: put video-filter mapping rejected", "index", index, "status", res.StatusCode)
	}
}

// ─── Video filters on posts_v1 ──────────────────────────────────────────

// Video search parameter values. Validation lives in ParseVideoSearchOptions
// so the handler and the tests agree on exactly one allowlist.
const (
	VideoSortRelevance = "relevance"
	VideoSortViews     = "views"
	VideoSortDate      = "date"

	VideoDurationShort  = "short"  // under 4 minutes
	VideoDurationMedium = "medium" // 4 to 20 minutes
	VideoDurationLong   = "long"   // over 20 minutes

	VideoDateHour  = "hour"
	VideoDateToday = "today"
	VideoDateWeek  = "week"
	VideoDateMonth = "month"
	VideoDateYear  = "year"

	VideoFeatureCC = "cc" // has subtitles
	VideoFeatureHD = "hd" // height >= 720
	VideoFeature4K = "4k" // height >= 2160
)

const (
	videoShortMaxMs  = 4 * 60 * 1000
	videoMediumMaxMs = 20 * 60 * 1000
	videoHDMinHeight = 720
	video4KMinHeight = 2160
)

// VideoSearchOptions is the parsed ?sort / ?duration / ?date / ?features
// set. The zero value is the unfiltered, relevance-ordered search.
type VideoSearchOptions struct {
	Sort     string
	Duration string
	Date     string
	Features []string
	// Now anchors the date filter; zero means time.Now(). Set by tests.
	Now time.Time
}

// IsZero reports whether the options change anything about the plain
// search.
func (o VideoSearchOptions) IsZero() bool {
	return (o.Sort == "" || o.Sort == VideoSortRelevance) && o.Duration == "" && o.Date == "" && len(o.Features) == 0
}

// ParseVideoSearchOptions validates the raw query values. Empty values are
// fine; an unknown value is an error whose message names the parameter and
// its allowed values, suitable for a 400 body. `features` is a
// comma-separated list; duplicates are folded, order is not significant.
func ParseVideoSearchOptions(sortBy, duration, date, features string) (VideoSearchOptions, error) {
	var o VideoSearchOptions
	switch v := strings.ToLower(strings.TrimSpace(sortBy)); v {
	case "", VideoSortRelevance:
		o.Sort = VideoSortRelevance
	case VideoSortViews, VideoSortDate:
		o.Sort = v
	default:
		return o, fmt.Errorf("Parameter 'sort' must be one of: relevance, views, date")
	}
	switch v := strings.ToLower(strings.TrimSpace(duration)); v {
	case "":
	case VideoDurationShort, VideoDurationMedium, VideoDurationLong:
		o.Duration = v
	default:
		return o, fmt.Errorf("Parameter 'duration' must be one of: short, medium, long")
	}
	switch v := strings.ToLower(strings.TrimSpace(date)); v {
	case "":
	case VideoDateHour, VideoDateToday, VideoDateWeek, VideoDateMonth, VideoDateYear:
		o.Date = v
	default:
		return o, fmt.Errorf("Parameter 'date' must be one of: hour, today, week, month, year")
	}
	if strings.TrimSpace(features) != "" {
		seen := map[string]bool{}
		for _, raw := range strings.Split(features, ",") {
			f := strings.ToLower(strings.TrimSpace(raw))
			if f == "" {
				continue
			}
			switch f {
			case VideoFeatureCC, VideoFeatureHD, VideoFeature4K:
			default:
				return o, fmt.Errorf("Parameter 'features' must be a comma-separated list of: cc, hd, 4k")
			}
			if !seen[f] {
				seen[f] = true
				o.Features = append(o.Features, f)
			}
		}
	}
	return o, nil
}

// videoFilterClauses turns the options into bool.filter clauses. Exported
// through BuildVideoSearchQuery for the tests; pure.
func videoFilterClauses(o VideoSearchOptions) []map[string]interface{} {
	var out []map[string]interface{}
	switch o.Duration {
	case VideoDurationShort:
		// gte 1: a document whose duration is unknown (0) is not "short",
		// it is unmeasured, and must not pad the short bucket.
		out = append(out, map[string]interface{}{"range": map[string]interface{}{"duration_ms": map[string]interface{}{"gte": 1, "lt": videoShortMaxMs}}})
	case VideoDurationMedium:
		out = append(out, map[string]interface{}{"range": map[string]interface{}{"duration_ms": map[string]interface{}{"gte": videoShortMaxMs, "lte": videoMediumMaxMs}}})
	case VideoDurationLong:
		out = append(out, map[string]interface{}{"range": map[string]interface{}{"duration_ms": map[string]interface{}{"gt": videoMediumMaxMs}}})
	}
	if o.Date != "" {
		now := o.Now
		if now.IsZero() {
			now = time.Now()
		}
		var since time.Time
		switch o.Date {
		case VideoDateHour:
			since = now.Add(-time.Hour)
		case VideoDateToday:
			since = now.Add(-24 * time.Hour)
		case VideoDateWeek:
			since = now.Add(-7 * 24 * time.Hour)
		case VideoDateMonth:
			since = now.AddDate(0, -1, 0)
		case VideoDateYear:
			since = now.AddDate(-1, 0, 0)
		}
		// created_at, not published_at: every document carries created_at,
		// while published_at exists only on documents written since the
		// field was added (or reindexed). For an unscheduled post the two
		// are the same instant; a scheduled post is indexed at publish time
		// with its row's created_at, which is the honest approximation
		// until the producer stamps published_at.
		out = append(out, map[string]interface{}{"range": map[string]interface{}{"created_at": map[string]interface{}{"gte": since.UTC().Format(time.RFC3339)}}})
	}
	for _, f := range o.Features {
		switch f {
		case VideoFeatureCC:
			out = append(out, map[string]interface{}{"term": map[string]interface{}{"has_subtitles": true}})
		case VideoFeatureHD:
			out = append(out, map[string]interface{}{"range": map[string]interface{}{"height": map[string]interface{}{"gte": videoHDMinHeight}}})
		case VideoFeature4K:
			out = append(out, map[string]interface{}{"range": map[string]interface{}{"height": map[string]interface{}{"gte": video4KMinHeight}}})
		}
	}
	return out
}

// videoSortClause is the `sort` block for the options; nil for relevance
// (OpenSearch's default, by _score).
func videoSortClause(o VideoSearchOptions) []interface{} {
	switch o.Sort {
	case VideoSortViews:
		return []interface{}{
			map[string]interface{}{"view_count": map[string]interface{}{"order": "desc", "missing": "_last", "unmapped_type": "long"}},
			"_score",
		}
	case VideoSortDate:
		return []interface{}{
			map[string]interface{}{"created_at": map[string]interface{}{"order": "desc"}},
			"_score",
		}
	}
	return nil
}

// BuildVideoSearchQuery is the query body SearchVideosFiltered sends: the
// same text match and public-approved filter as SearchPostsFiltered, plus
// the content-type terms and the video filters. Pure, for the tests.
func BuildVideoSearchQuery(query string, contentTypes []string, o VideoSearchOptions, limit int) map[string]interface{} {
	filter := publicApprovedFilter()
	if len(contentTypes) > 0 {
		filter = append(filter, map[string]interface{}{
			"terms": map[string]interface{}{"content_type": contentTypes},
		})
	}
	filter = append(filter, videoFilterClauses(o)...)
	q := map[string]interface{}{
		"size": limit,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{"multi_match": map[string]interface{}{
						"query":  query,
						"fields": []string{"title^3", "text^2", "hashtags"},
					}},
				},
				"filter": filter,
			},
		},
	}
	if sortClause := videoSortClause(o); sortClause != nil {
		q["sort"] = sortClause
	}
	return q
}

// SearchVideosFiltered is SearchPostsFiltered with the video options. With
// zero options it sends exactly what SearchPostsFiltered sends.
func (s *Store) SearchVideosFiltered(ctx context.Context, query string, contentTypes []string, o VideoSearchOptions, limit int) ([]PostDoc, error) {
	return s.execPostSearch(ctx, BuildVideoSearchQuery(query, contentTypes, o, limit))
}

// IncrementPostViewCount adds delta to a post document's view_count in
// place (a scripted update, like AddToEngagementScore). A missing document
// is not an error: views on a post that is not publicly searchable are
// not search's business.
func (s *Store) IncrementPostViewCount(ctx context.Context, postID string, delta int64) error {
	if postID == "" {
		return nil
	}
	body := map[string]any{
		"script": map[string]any{
			"source": "ctx._source.view_count = (ctx._source.view_count == null ? 0 : ctx._source.view_count) + params.delta",
			"params": map[string]any{"delta": delta},
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
		return fmt.Errorf("update view_count on %s/%s: %s", IndexPosts, postID, res.String())
	}
	return nil
}

// ─── Tube channels ──────────────────────────────────────────────────────

// BulkIndexTubeChannels upserts channel documents by channel_id.
func (s *Store) BulkIndexTubeChannels(ctx context.Context, docs []TubeChannelDoc) (int, error) {
	ids := make([]string, len(docs))
	anys := make([]any, len(docs))
	for i, d := range docs {
		ids[i], anys[i] = d.ChannelID, d
	}
	return s.bulkIndexDocs(ctx, IndexTubeChannels, ids, anys)
}

// DeleteTubeChannel is idempotent (404 is success).
func (s *Store) DeleteTubeChannel(ctx context.Context, channelID string) error {
	return s.deleteDoc(ctx, IndexTubeChannels, channelID)
}

// BuildTubeChannelQuery: name and handle carry the match (handle exactly,
// or by prefix through its standard-analysed subfield), about is a weak
// tiebreaker; ties by follower count. Pure, for the tests.
func BuildTubeChannelQuery(query string, limit, from int) map[string]interface{} {
	handle := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(query)), "@")
	return map[string]interface{}{
		"size": limit,
		"from": from,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"should": []map[string]interface{}{
					{"term": map[string]interface{}{"handle": map[string]interface{}{"value": handle, "boost": 6}}},
					{"prefix": map[string]interface{}{"handle": map[string]interface{}{"value": handle, "boost": 3}}},
					{"multi_match": map[string]interface{}{
						"query":  query,
						"fields": []string{"name^3", "handle.prefix^2", "about"},
					}},
				},
				"minimum_should_match": 1,
			},
		},
		"sort": []interface{}{
			"_score",
			map[string]interface{}{"follower_count": map[string]interface{}{"order": "desc"}},
		},
	}
}

// SearchTubeChannels runs BuildTubeChannelQuery against tube_channels_v1
// with the viewer's block scope applied on owner_id.
func (s *Store) SearchTubeChannels(ctx context.Context, query string, limit, from int) ([]TubeChannelDoc, error) {
	raw, err := s.execGenericSearch(ctx, IndexTubeChannels, BuildTubeChannelQuery(query, limit, from))
	if err != nil {
		return nil, err
	}
	out := make([]TubeChannelDoc, 0, len(raw))
	for _, src := range raw {
		var d TubeChannelDoc
		if b, err := json.Marshal(src); err == nil {
			_ = json.Unmarshal(b, &d)
		}
		if d.ChannelID != "" {
			out = append(out, d)
		}
	}
	return out, nil
}

// ─── Tube collections (public playlists) ────────────────────────────────

// BulkIndexTubeCollections upserts playlist documents by playlist_id. It
// REFUSES a non-public document rather than writing it: the reindex is
// supposed to delete those, and a caller that hands one over has a bug
// that must not become a leak.
func (s *Store) BulkIndexTubeCollections(ctx context.Context, docs []TubeCollectionDoc) (int, error) {
	ids := make([]string, 0, len(docs))
	anys := make([]any, 0, len(docs))
	for _, d := range docs {
		if d.Visibility != "public" {
			return 0, fmt.Errorf("tube collections: refusing to index non-public playlist %s (%s)", d.PlaylistID, d.Visibility)
		}
		ids = append(ids, d.PlaylistID)
		anys = append(anys, d)
	}
	return s.bulkIndexDocs(ctx, IndexTubeCollections, ids, anys)
}

// DeleteTubeCollection is idempotent (404 is success).
func (s *Store) DeleteTubeCollection(ctx context.Context, playlistID string) error {
	return s.deleteDoc(ctx, IndexTubeCollections, playlistID)
}

// BuildTubeCollectionQuery: title carries the match, description is the
// tiebreaker, and ONLY public playlists are searched — the query-time half
// of the visibility rule (see the file comment). Pure, for the tests.
func BuildTubeCollectionQuery(query string, limit, from int) map[string]interface{} {
	return map[string]interface{}{
		"size": limit,
		"from": from,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{"multi_match": map[string]interface{}{
						"query":  query,
						"fields": []string{"title^3", "description"},
					}},
				},
				"filter": []map[string]interface{}{
					{"term": map[string]interface{}{"visibility": "public"}},
				},
			},
		},
		"sort": []interface{}{
			"_score",
			map[string]interface{}{"item_count": map[string]interface{}{"order": "desc"}},
		},
	}
}

// SearchTubeCollections runs BuildTubeCollectionQuery against
// tube_collections_v1 with the viewer's block scope applied on owner_id.
func (s *Store) SearchTubeCollections(ctx context.Context, query string, limit, from int) ([]TubeCollectionDoc, error) {
	raw, err := s.execGenericSearch(ctx, IndexTubeCollections, BuildTubeCollectionQuery(query, limit, from))
	if err != nil {
		return nil, err
	}
	out := make([]TubeCollectionDoc, 0, len(raw))
	for _, src := range raw {
		var d TubeCollectionDoc
		if b, err := json.Marshal(src); err == nil {
			_ = json.Unmarshal(b, &d)
		}
		// Belt to the query's braces: a document that is not public is
		// dropped even if the index somehow holds it.
		if d.PlaylistID != "" && d.Visibility == "public" {
			out = append(out, d)
		}
	}
	return out, nil
}

// RefreshIndex makes recent writes to one index searchable now, so a
// reindex can report a count a query would agree with.
func (s *Store) RefreshIndex(ctx context.Context, index string) error {
	req := opensearchapi.IndicesRefreshRequest{Index: []string{index}}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("refresh %s: %s", index, res.String())
	}
	return nil
}

// bulkIndexDocs is the shared bulk upsert: one _bulk call per
// bulkChunkSize, per-item statuses parsed so a partial failure is counted
// honestly (the BulkIndexUsers rule).
func (s *Store) bulkIndexDocs(ctx context.Context, index string, ids []string, docs []any) (int, error) {
	if len(docs) == 0 {
		return 0, nil
	}
	if len(docs) > bulkChunkSize {
		total := 0
		for i := 0; i < len(docs); i += bulkChunkSize {
			end := i + bulkChunkSize
			if end > len(docs) {
				end = len(docs)
			}
			n, err := s.bulkIndexDocs(ctx, index, ids[i:end], docs[i:end])
			total += n
			if err != nil {
				return total, err
			}
		}
		return total, nil
	}
	var buf bytes.Buffer
	for i, doc := range docs {
		if ids[i] == "" {
			return 0, fmt.Errorf("bulk index %s: empty document id at position %d", index, i)
		}
		meta, _ := json.Marshal(map[string]any{"index": map[string]any{"_index": index, "_id": ids[i]}})
		buf.Write(meta)
		buf.WriteByte('\n')
		data, err := json.Marshal(doc)
		if err != nil {
			return 0, err
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}
	res, err := s.client.Bulk(bytes.NewReader(buf.Bytes()), s.client.Bulk.WithContext(ctx))
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	if res.IsError() {
		return 0, fmt.Errorf("bulk index %s error: %s", index, res.String())
	}
	var bulkResp struct {
		Errors bool `json:"errors"`
		Items  []map[string]struct {
			Status int    `json:"status"`
			Error  any    `json:"error,omitempty"`
			ID     string `json:"_id"`
		} `json:"items"`
	}
	if err := json.NewDecoder(res.Body).Decode(&bulkResp); err != nil {
		slog.Warn("search: bulk index response unparseable, assuming success", "index", index, "error", err, "count", len(docs))
		return len(docs), nil
	}
	if !bulkResp.Errors {
		return len(docs), nil
	}
	succeeded := 0
	for _, item := range bulkResp.Items {
		for op, st := range item {
			if st.Status >= 200 && st.Status < 300 {
				succeeded++
			} else {
				slog.Warn("search: bulk doc failed", "index", index, "op", op, "id", st.ID, "status", st.Status, "error", st.Error)
			}
		}
	}
	return succeeded, nil
}

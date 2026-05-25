package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
)

type Store struct {
	client *opensearch.Client
}

func New(url string) (*Store, error) {
	client, err := opensearch.NewClient(opensearch.Config{
		Addresses: []string{url},
		// Disable verification for dev/self-signed certs if needed,
		// but standard docker image is HTTP by default or easy to configure
	})
	if err != nil {
		return nil, err
	}

	s := &Store{client: client}
	s.initIndices()
	return s, nil
}

// newBgCtx is a small helper so mappings.go can share the same
// background ctx pattern without re-importing context.
func newBgCtx() context.Context { return context.Background() }

func (s *Store) initIndices() {
	ctx := context.Background()
	// Audit HS5 + HS6: shards / replicas / refresh_interval are
	// env-tunable so prod deploys can run shards=3/replicas=1/refresh=10s
	// (parallel reads, HA, balanced indexer load) without flipping a
	// single-node dev cluster into a permanently-yellow state. Existing
	// indices keep their original settings until a settings-migration
	// runs — createIndexIfNotExists is a no-op for present indices.
	settings := opensearchSettingsJSON()

	// Users Index
	s.createIndexIfNotExists(ctx, "users_v1", `{
		"settings": `+settings+`,
		"mappings": {
			"properties": {
				"user_id":         { "type": "keyword" },
				"username":        { "type": "text", "fields": { "keyword": { "type": "keyword" } } },
				"display_name":    { "type": "text" },
				"bio":             { "type": "text" },
				"avatar_media_id": { "type": "keyword" },
				"is_verified":     { "type": "boolean" }
			}
		}
	}`)

	// Posts Index
	s.createIndexIfNotExists(ctx, "posts_v1", `{
		"settings": `+settings+`,
		"mappings": {
			"properties": {
				"post_id":         { "type": "keyword" },
				"author_id":       { "type": "keyword" },
				"author_username": { "type": "keyword" },
				"text":            { "type": "text" },
				"hashtags":        { "type": "keyword" },
				"visibility":      { "type": "keyword" },
				"like_count":      { "type": "long" },
				"comment_count":   { "type": "long" },
				"post_type":       { "type": "keyword" },
				"app_origin":      { "type": "keyword" },
				"created_at":      { "type": "date" }
			}
		}
	}`)

	// Products index
	s.createIndexIfNotExists(ctx, "products_v1", `{
		"settings": `+settings+`,
		"mappings": {
			"properties": {
				"product_id":   { "type": "keyword" },
				"title":        { "type": "text", "analyzer": "english" },
				"description":  { "type": "text" },
				"category":     { "type": "keyword" },
				"price":        { "type": "float" },
				"city":         { "type": "keyword" },
				"seller_id":    { "type": "keyword" },
				"status":       { "type": "keyword" }
			}
		}
	}`)

	// Events index
	s.createIndexIfNotExists(ctx, "events_v1", `{
		"settings": `+settings+`,
		"mappings": {
			"properties": {
				"event_id":     { "type": "keyword" },
				"title":        { "type": "text" },
				"description":  { "type": "text" },
				"starts_at":    { "type": "date" },
				"status":       { "type": "keyword" }
			}
		}
	}`)

	// Messages index (search within chat)
	s.createIndexIfNotExists(ctx, "messages_v1", `{
		"settings": `+settings+`,
		"mappings": {
			"properties": {
				"message_id":       { "type": "keyword" },
				"conversation_id":  { "type": "keyword" },
				"sender_id":        { "type": "keyword" },
				"text":             { "type": "text", "analyzer": "english" },
				"ts":               { "type": "date" }
			}
		}
	}`)

	// Six-entity relevance system: hashtags / communities / channels
	// indices + engagement_score mappings layered onto users/posts/
	// products. See mappings.go.
	s.initEntityIndices()
}

// putEngagementMapping idempotently adds `engagement_score` (double)
// to an existing index. OpenSearch put-mapping is additive — it cannot
// change a field's type but can introduce new fields. Safe to call on
// every boot.
func (s *Store) putEngagementMapping(ctx context.Context, index string) {
	body := `{"properties":{"engagement_score":{"type":"double"}}}`
	req := opensearchapi.IndicesPutMappingRequest{
		Index: []string{index},
		Body:  strings.NewReader(body),
	}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		slog.Warn("opensearch: put engagement_score mapping failed", "index", index, "err", err)
		return
	}
	defer res.Body.Close()
	if res.IsError() {
		slog.Warn("opensearch: put engagement_score mapping rejected", "index", index, "status", res.StatusCode)
	}
}

// opensearchSettingsJSON returns the per-index settings block. Reads
// OPENSEARCH_INDEX_SHARDS / OPENSEARCH_INDEX_REPLICAS / OPENSEARCH_INDEX_REFRESH
// with safe dev defaults (1/0/1s).
func opensearchSettingsJSON() string {
	shards := envOrDefault("OPENSEARCH_INDEX_SHARDS", "1")
	replicas := envOrDefault("OPENSEARCH_INDEX_REPLICAS", "0")
	refresh := envOrDefault("OPENSEARCH_INDEX_REFRESH", "1s")
	return fmt.Sprintf(`{ "number_of_shards": %s, "number_of_replicas": %s, "refresh_interval": "%s" }`,
		shards, replicas, refresh)
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// regexEscape escapes OpenSearch regex metacharacters so a user-
// supplied prefix can be safely spliced into a `include` regex.
// OpenSearch regex syntax mirrors Lucene's; the set below covers
// every reserved character.
func regexEscape(s string) string {
	const meta = `.+*?(){}[]|\^$"`
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if strings.ContainsRune(meta, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (s *Store) createIndexIfNotExists(ctx context.Context, index, body string) {
	exists, err := opensearchapi.IndicesExistsRequest{
		Index: []string{index},
	}.Do(ctx, s.client)

	if err != nil {
		slog.Error("error checking search index", "index", index, "error", err)
		return
	}
	defer exists.Body.Close()

	if exists.StatusCode == 404 {
		create, err := opensearchapi.IndicesCreateRequest{
			Index: index,
			Body:  strings.NewReader(body),
		}.Do(ctx, s.client)
		if err != nil {
			slog.Error("error creating search index", "index", index, "error", err)
			return
		}
		defer create.Body.Close()
		slog.Info("created search index", "index", index)
	}
}

// Structs for Documents
type UserDoc struct {
	UserID          string  `json:"user_id"`
	Username        string  `json:"username"`
	DisplayName     string  `json:"display_name"`
	Bio             string  `json:"bio"`
	AvatarMediaID   string  `json:"avatar_media_id,omitempty"`
	IsVerified      bool    `json:"is_verified"`
	FollowerCount   int     `json:"follower_count,omitempty"`
	PostCount       int     `json:"post_count,omitempty"`
	EngagementScore float64 `json:"engagement_score"`
}

type PostDoc struct {
	PostID          string    `json:"post_id"`
	AuthorID        string    `json:"author_id"`
	AuthorUsername  string    `json:"author_username,omitempty"`
	Text            string    `json:"text"`
	Hashtags        []string  `json:"hashtags,omitempty"`
	Visibility      string    `json:"visibility,omitempty"`
	LikeCount       int       `json:"like_count"`
	CommentCount    int       `json:"comment_count"`
	ShareCount      int       `json:"share_count,omitempty"`
	BookmarkCount   int       `json:"bookmark_count,omitempty"`
	PostType        string    `json:"post_type,omitempty"`
	AppOrigin       string    `json:"app_origin,omitempty"`
	EngagementScore float64   `json:"engagement_score"`
	CreatedAt       time.Time `json:"created_at"`
}

// HashtagDoc represents one hashtag in the hashtags_v1 index. Keyed
// by the lowercase hashtag string.
type HashtagDoc struct {
	Hashtag         string    `json:"hashtag"`
	HashtagSearch   string    `json:"hashtag_search"`
	UseCount        int       `json:"use_count"`
	EngagementScore float64   `json:"engagement_score"`
	CreatedAt       time.Time `json:"created_at"`
}

// CommunityDoc represents one community in the communities_v1 index.
type CommunityDoc struct {
	CommunityID     string    `json:"community_id"`
	OwnerID         string    `json:"owner_id"`
	Handle          string    `json:"handle"`
	Name            string    `json:"name"`
	Description     string    `json:"description,omitempty"`
	CommunityType   string    `json:"community_type"`
	Category        string    `json:"category,omitempty"`
	TopicTags       []string  `json:"topic_tags,omitempty"`
	MemberCount     int       `json:"member_count"`
	IsVerified      bool      `json:"is_verified"`
	EngagementScore float64   `json:"engagement_score"`
	CreatedAt       time.Time `json:"created_at"`
}

// ChannelDoc represents one broadcast channel in channels_v1.
type ChannelDoc struct {
	ChannelID       string    `json:"channel_id"`
	OwnerID         string    `json:"owner_id"`
	Handle          string    `json:"handle"`
	Name            string    `json:"name"`
	Description     string    `json:"description,omitempty"`
	ChannelType     string    `json:"channel_type"`
	Category        string    `json:"category,omitempty"`
	SubscriberCount int       `json:"subscriber_count"`
	IsVerified      bool      `json:"is_verified"`
	EngagementScore float64   `json:"engagement_score"`
	CreatedAt       time.Time `json:"created_at"`
}

// ProductDoc represents one product in products_v1. The original
// products mapping was created with a small set of fields and we
// continue to index via a flexible map[string]any, but ProductDoc is
// the canonical shape used by the backfill + ranked-search response.
type ProductDoc struct {
	ProductID       string    `json:"product_id"`
	SellerID        string    `json:"seller_id"`
	Title           string    `json:"title"`
	Description     string    `json:"description,omitempty"`
	Category        string    `json:"category,omitempty"`
	Price           float64   `json:"price,omitempty"`
	City            string    `json:"city,omitempty"`
	Status          string    `json:"status,omitempty"`
	ViewCount       int       `json:"view_count,omitempty"`
	OrderCount      int       `json:"order_count,omitempty"`
	EngagementScore float64   `json:"engagement_score"`
	CreatedAt       time.Time `json:"created_at,omitempty"`
}

// UniversalSearchResult holds combined results from users and posts.
type UniversalSearchResult struct {
	Users []UserDoc `json:"users"`
	Posts []PostDoc `json:"posts"`
}

// IndexUser
func (s *Store) IndexUser(ctx context.Context, doc UserDoc) error {
	data, _ := json.Marshal(doc)
	req := opensearchapi.IndexRequest{
		Index:      "users_v1",
		DocumentID: doc.UserID,
		Body:       bytes.NewReader(data),
		// Audit HS1: dropped Refresh: "true". Forcing a refresh on every
		// write blocked the post-create / user-update path on
		// OpenSearch's refresh cycle and starved the indexer. The
		// index-level refresh_interval (set in createIndexIfNotExists)
		// surfaces new docs within a few seconds — fine for search.
	}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("indexing error: %s", res.String())
	}
	return nil
}

// IndexPost
func (s *Store) IndexPost(ctx context.Context, doc PostDoc) error {
	data, _ := json.Marshal(doc)
	req := opensearchapi.IndexRequest{
		Index:      "posts_v1",
		DocumentID: doc.PostID,
		Body:       bytes.NewReader(data),
	}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("indexing error: %s", res.String())
	}
	return nil
}

// bulkChunkSize caps the number of docs we hand to OpenSearch in one
// HTTP request. 500 keeps the request body well under the default 100
// MB body-size limit and lets the client recover (next chunk) if a
// single chunk fails. MS7: prior to this, a caller passing a 100k-doc
// slice would blow the heap building the buffer + risk OpenSearch
// rejecting the body for size.
const bulkChunkSize = 500

// BulkIndexUsers indexes multiple users. Chunks at bulkChunkSize so a
// large slice doesn't pin a multi-MB buffer in memory or risk OpenSearch
// payload-size rejections. Returns the total successfully-indexed count
// across all chunks; a chunk failure aborts the remaining chunks (the
// returned count covers what landed before the failure).
func (s *Store) BulkIndexUsers(ctx context.Context, docs []UserDoc) (int, error) {
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
			n, err := s.BulkIndexUsers(ctx, docs[i:end])
			total += n
			if err != nil {
				return total, err
			}
		}
		return total, nil
	}

	var buf bytes.Buffer
	for _, doc := range docs {
		meta := fmt.Sprintf(`{"index":{"_index":"users_v1","_id":"%s"}}`, doc.UserID)
		buf.WriteString(meta)
		buf.WriteByte('\n')
		data, _ := json.Marshal(doc)
		buf.Write(data)
		buf.WriteByte('\n')
	}

	res, err := s.client.Bulk(
		bytes.NewReader(buf.Bytes()),
		s.client.Bulk.WithContext(ctx),
		// Audit HS1: previously WithRefresh("true") — see IndexUser comment.
	)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()

	if res.IsError() {
		return 0, fmt.Errorf("bulk index error: %s", res.String())
	}

	// Audit HS8: parse the per-doc bulk response so partial failures
	// don't silently inflate the success count. OpenSearch returns
	// HTTP 200 even when some items 4xx/5xx individually.
	var bulkResp struct {
		Errors bool `json:"errors"`
		Items  []map[string]struct {
			Status int    `json:"status"`
			Error  any    `json:"error,omitempty"`
			ID     string `json:"_id"`
		} `json:"items"`
	}
	if err := json.NewDecoder(res.Body).Decode(&bulkResp); err != nil {
		// Body parse failed but HTTP was 200 — fall back to assume
		// success, but log so ops sees the visibility gap.
		slog.Warn("search: bulk index response unparseable, assuming success", "error", err, "count", len(docs))
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
				slog.Warn("search: bulk doc failed", "op", op, "id", st.ID, "status", st.Status, "error", st.Error)
			}
		}
	}
	return succeeded, nil
}

// CountUsers returns the number of documents in users_v1. Used by the
// startup auto-heal check — a count of 0 means the index was wiped or
// freshly created and needs a reconciliation pass from profile-service.
func (s *Store) CountUsers(ctx context.Context) (int64, error) {
	res, err := s.client.Count(
		s.client.Count.WithContext(ctx),
		s.client.Count.WithIndex("users_v1"),
	)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	if res.IsError() {
		return 0, fmt.Errorf("count error: %s", res.String())
	}
	var r struct {
		Count int64 `json:"count"`
	}
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		return 0, err
	}
	return r.Count, nil
}

// SearchUsers performs prefix + fuzzy search across username, display_name, bio.
func (s *Store) SearchUsers(ctx context.Context, query string, limit int) ([]UserDoc, error) {
	q := map[string]interface{}{
		"size": limit,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"should": []interface{}{
					// Prefix match on username (highest boost)
					map[string]interface{}{
						"prefix": map[string]interface{}{
							"username": map[string]interface{}{
								"value": strings.ToLower(query),
								"boost": 5,
							},
						},
					},
					// Prefix match on display_name
					map[string]interface{}{
						"match_phrase_prefix": map[string]interface{}{
							"display_name": map[string]interface{}{
								"query": query,
								"boost": 3,
							},
						},
					},
					// Fuzzy match as fallback
					map[string]interface{}{
						"multi_match": map[string]interface{}{
							"query":     query,
							"fields":    []string{"display_name^2", "username^3", "bio"},
							"fuzziness": "AUTO",
						},
					},
				},
				"minimum_should_match": 1,
			},
		},
	}

	return s.execUserSearch(ctx, q)
}

func (s *Store) execUserSearch(ctx context.Context, query interface{}) ([]UserDoc, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(query); err != nil {
		return nil, err
	}

	res, err := s.client.Search(
		s.client.Search.WithContext(ctx),
		s.client.Search.WithIndex("users_v1"),
		s.client.Search.WithBody(&buf),
	)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("search error: %s", res.String())
	}

	var r map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		return nil, err
	}

	var docs []UserDoc
	hits := r["hits"].(map[string]interface{})["hits"].([]interface{})
	for _, hit := range hits {
		source := hit.(map[string]interface{})["_source"]
		b, _ := json.Marshal(source)
		var doc UserDoc
		json.Unmarshal(b, &doc)
		docs = append(docs, doc)
	}
	return docs, nil
}

// SearchPosts
func (s *Store) SearchPosts(ctx context.Context, query string, limit int) ([]PostDoc, error) {
	return s.SearchPostsFiltered(ctx, query, nil, limit)
}

// SearchPostsFiltered runs a text-match search with an optional content_type
// filter. Pass {"long_video"} for the Posttube video search tab, {"flick"}
// for Reels, or {"long_video","flick"} to blend both. nil/empty means
// "all content types" (same as SearchPosts).
//
// Implementation: bool query with a `must` text match + an optional
// `terms` filter on content_type. Cheaper than running per-type searches
// because OpenSearch handles the term-level filter via doc_values.
//
// Audit CS1: a mandatory visibility=public filter is always applied.
// Without it, friends-only / circle / private posts were returned to
// any caller. Per-viewer visibility (friends, circles, mutuals) is
// out of scope here — `public` is the only safe default for an
// unauthenticated/cross-graph search endpoint. Feed-service handles
// the richer per-viewer visibility model for ranked/home timelines.
func (s *Store) SearchPostsFiltered(ctx context.Context, query string, contentTypes []string, limit int) ([]PostDoc, error) {
	mustClauses := []map[string]interface{}{
		{"match": map[string]interface{}{"text": query}},
	}
	filter := []map[string]interface{}{
		{"term": map[string]interface{}{"visibility": "public"}},
	}
	if len(contentTypes) > 0 {
		filter = append(filter, map[string]interface{}{
			"terms": map[string]interface{}{"content_type": contentTypes},
		})
	}

	q := map[string]interface{}{
		"size": limit,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must":   mustClauses,
				"filter": filter,
			},
		},
	}
	return s.execPostSearch(ctx, q)
}

func (s *Store) execPostSearch(ctx context.Context, query interface{}) ([]PostDoc, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(query); err != nil {
		return nil, err
	}

	res, err := s.client.Search(
		s.client.Search.WithContext(ctx),
		s.client.Search.WithIndex("posts_v1"),
		s.client.Search.WithBody(&buf),
	)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("search error: %s", res.String())
	}

	var r map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		return nil, err
	}

	var docs []PostDoc
	hits := r["hits"].(map[string]interface{})["hits"].([]interface{})
	for _, hit := range hits {
		source := hit.(map[string]interface{})["_source"]
		b, _ := json.Marshal(source)
		var doc PostDoc
		json.Unmarshal(b, &doc)
		docs = append(docs, doc)
	}
	return docs, nil
}

// SearchHashtags performs a prefix-filtered terms aggregation on the hashtags field
// and returns the top matching hashtag strings for autocomplete.
//
// Audit HS4: previously the prefix was injected verbatim into a regex
// (`include`: `<prefix>.*`) with no length or character validation. A
// 1-character prefix made OpenSearch enumerate the full hashtag
// keyword space; a crafted prefix with regex metacharacters could
// cost still more. Now: require ≥2 characters, escape metacharacters,
// cap aggregation size, and apply a request-side timeout.
func (s *Store) SearchHashtags(ctx context.Context, prefix string, limit int) ([]string, error) {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if len(prefix) < 2 {
		return []string{}, nil
	}
	if len(prefix) > 32 {
		prefix = prefix[:32]
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	// Escape OpenSearch regex metacharacters before splicing into `include`.
	escaped := regexEscape(prefix)

	q := map[string]interface{}{
		"size":    0, // No document hits needed, only aggregation results
		"timeout": "2s", // MS1: bound cluster-side time
		"query": map[string]interface{}{
			"prefix": map[string]interface{}{
				"hashtags": prefix,
			},
		},
		"aggs": map[string]interface{}{
			"hashtag_counts": map[string]interface{}{
				"terms": map[string]interface{}{
					"field":   "hashtags",
					"size":    limit,
					"include": fmt.Sprintf("%s.*", escaped),
				},
			},
		},
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(q); err != nil {
		return nil, err
	}

	// MS1: hard caller-side deadline so a misbehaving cluster can't
	// pin the goroutine past the 2s OpenSearch timeout.
	tCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	res, err := s.client.Search(
		s.client.Search.WithContext(tCtx),
		s.client.Search.WithIndex("posts_v1"),
		s.client.Search.WithBody(&buf),
	)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("hashtag search error: %s", res.String())
	}

	var r map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		return nil, err
	}

	var hashtags []string
	aggs, ok := r["aggregations"].(map[string]interface{})
	if !ok {
		return hashtags, nil
	}
	buckets, ok := aggs["hashtag_counts"].(map[string]interface{})["buckets"].([]interface{})
	if !ok {
		return hashtags, nil
	}
	for _, b := range buckets {
		bucket := b.(map[string]interface{})
		if key, ok := bucket["key"].(string); ok {
			hashtags = append(hashtags, key)
		}
	}
	return hashtags, nil
}

// UniversalSearch searches across users_v1 and posts_v1 and returns combined results.
// searchType can be "all", "profiles", or "posts".
func (s *Store) UniversalSearch(ctx context.Context, query string, searchType string, limit int) (*UniversalSearchResult, error) {
	result := &UniversalSearchResult{
		Users: []UserDoc{},
		Posts: []PostDoc{},
	}

	switch searchType {
	case "profiles":
		users, err := s.SearchUsers(ctx, query, limit)
		if err != nil {
			return nil, fmt.Errorf("user search failed: %w", err)
		}
		if users != nil {
			result.Users = users
		}

	case "posts":
		posts, err := s.SearchPosts(ctx, query, limit)
		if err != nil {
			return nil, fmt.Errorf("post search failed: %w", err)
		}
		if posts != nil {
			result.Posts = posts
		}

	case "videos":
		// Long-form video tab on the Posttube search surface.
		posts, err := s.SearchPostsFiltered(ctx, query, []string{"long_video"}, limit)
		if err != nil {
			return nil, fmt.Errorf("video search failed: %w", err)
		}
		if posts != nil {
			result.Posts = posts
		}

	case "flicks":
		// Short-form vertical tab on the Reels search surface.
		posts, err := s.SearchPostsFiltered(ctx, query, []string{"flick"}, limit)
		if err != nil {
			return nil, fmt.Errorf("flick search failed: %w", err)
		}
		if posts != nil {
			result.Posts = posts
		}

	default: // "all"
		// Run user and post searches with half the limit each to balance results
		halfLimit := limit / 2
		if halfLimit < 1 {
			halfLimit = 1
		}

		users, err := s.SearchUsers(ctx, query, halfLimit)
		if err != nil {
			slog.Error("universal search users error", "error", err)
		} else if users != nil {
			result.Users = users
		}

		posts, err := s.SearchPosts(ctx, query, halfLimit)
		if err != nil {
			slog.Error("universal search posts error", "error", err)
		} else if posts != nil {
			result.Posts = posts
		}
	}

	return result, nil
}

// DeletePost removes a post document from the OpenSearch index.
func (s *Store) DeletePost(ctx context.Context, postID string) error {
	req := opensearchapi.DeleteRequest{
		Index:      "posts_v1",
		DocumentID: postID,
	}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		slog.Error("opensearch: failed to delete post", "post_id", postID, "error", err)
		return err
	}
	defer res.Body.Close()
	if res.IsError() && res.StatusCode != 404 {
		err = fmt.Errorf("opensearch delete post error: %s", res.String())
		slog.Error("opensearch: failed to delete post", "post_id", postID, "error", err)
		return err
	}
	return nil
}

// DeletePostsByAuthor removes all post documents by a given author.
func (s *Store) DeletePostsByAuthor(ctx context.Context, authorID string) error {
	query := map[string]interface{}{
		"query": map[string]interface{}{
			"term": map[string]interface{}{"author_id": authorID},
		},
	}
	body, _ := json.Marshal(query)
	res, err := s.client.DeleteByQuery(
		[]string{"posts_v1"},
		bytes.NewReader(body),
		s.client.DeleteByQuery.WithContext(ctx),
	)
	if err != nil {
		slog.Error("opensearch: failed to delete posts by author", "author_id", authorID, "error", err)
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		err = fmt.Errorf("opensearch delete by query error: %s", res.String())
		slog.Error("opensearch: failed to delete posts by author", "author_id", authorID, "error", err)
		return err
	}
	return nil
}

// DeleteUser removes a user document from the OpenSearch index.
func (s *Store) DeleteUser(ctx context.Context, userID string) error {
	req := opensearchapi.DeleteRequest{
		Index:      "users_v1",
		DocumentID: userID,
	}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		slog.Error("opensearch: failed to delete user", "user_id", userID, "error", err)
		return err
	}
	defer res.Body.Close()
	if res.IsError() && res.StatusCode != 404 {
		err = fmt.Errorf("opensearch delete user error: %s", res.String())
		slog.Error("opensearch: failed to delete user", "user_id", userID, "error", err)
		return err
	}
	return nil
}

// UpdateUserUsername performs a partial update on a user document to change the username field.
func (s *Store) UpdateUserUsername(ctx context.Context, userID, newUsername string) error {
	doc := map[string]interface{}{
		"doc": map[string]interface{}{
			"username": newUsername,
		},
	}
	body, _ := json.Marshal(doc)
	req := opensearchapi.UpdateRequest{
		Index:      "users_v1",
		DocumentID: userID,
		Body:       bytes.NewReader(body),
	}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		slog.Error("opensearch: failed to update user username", "user_id", userID, "error", err)
		return err
	}
	defer res.Body.Close()
	if res.IsError() && res.StatusCode != 404 {
		err = fmt.Errorf("opensearch update user error: %s", res.String())
		slog.Error("opensearch: failed to update user username", "user_id", userID, "error", err)
		return err
	}
	return nil
}

// AutocompleteResult represents a single autocomplete suggestion. The
// Kind field discriminates between user/hashtag/community matches in
// the merged AutocompleteMulti response.
type AutocompleteResult struct {
	Kind        string `json:"kind,omitempty"` // "user" | "hashtag" | "community"
	UserID      string `json:"user_id,omitempty"`
	Username    string `json:"username,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Hashtag     string `json:"hashtag,omitempty"`
	CommunityID string `json:"community_id,omitempty"`
	Handle      string `json:"handle,omitempty"`
	Name        string `json:"name,omitempty"`
}

// Autocomplete returns username suggestions for the given prefix.
func (s *Store) Autocomplete(ctx context.Context, prefix string, limit int) ([]AutocompleteResult, error) {
	if limit <= 0 || limit > 20 {
		limit = 10
	}

	// MS1: bound the per-request cluster time so a slow prefix scan
	// can't pin a worker indefinitely. OpenSearch returns partial
	// results + timed_out=true on hit; the handler treats that as
	// success rather than 500ing.
	query := map[string]interface{}{
		"size":    limit,
		"timeout": "2s",
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"should": []interface{}{
					map[string]interface{}{
						"prefix": map[string]interface{}{
							"username": map[string]interface{}{
								"value": strings.ToLower(prefix),
								"boost": 2.0,
							},
						},
					},
					map[string]interface{}{
						"match_phrase_prefix": map[string]interface{}{
							"display_name": prefix,
						},
					},
				},
				"minimum_should_match": 1,
			},
		},
		"_source": []string{"user_id", "username", "display_name"},
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(query); err != nil {
		return nil, err
	}

	// Defense-in-depth: also wrap the caller ctx with a hard deadline
	// so a misbehaving OpenSearch that ignores `timeout` can't hold
	// the goroutine. 3s leaves headroom over the 2s cluster timeout.
	tCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	res, err := s.client.Search(
		s.client.Search.WithContext(tCtx),
		s.client.Search.WithIndex("users_v1"),
		s.client.Search.WithBody(&buf),
	)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("autocomplete search error: %s", res.String())
	}

	var searchResp struct {
		Hits struct {
			Hits []struct {
				Source AutocompleteResult `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(res.Body).Decode(&searchResp); err != nil {
		return nil, err
	}

	results := make([]AutocompleteResult, 0, len(searchResp.Hits.Hits))
	for _, h := range searchResp.Hits.Hits {
		r := h.Source
		r.Kind = "user"
		results = append(results, r)
	}
	return results, nil
}

// AutocompleteMulti returns merged suggestions across users, hashtags,
// and communities. Per-bucket cap is roughly limit/3 so the total
// response stays small. Each result carries Kind = "user" | "hashtag"
// | "community". An empty bucket on a partial index failure is logged
// but doesn't fail the whole call.
func (s *Store) AutocompleteMulti(ctx context.Context, prefix string, limit int) ([]AutocompleteResult, error) {
	if limit <= 0 || limit > 20 {
		limit = 10
	}
	per := limit / 3
	if per < 2 {
		per = 2
	}

	out := make([]AutocompleteResult, 0, limit)

	// Users — reuse the existing call.
	users, err := s.Autocomplete(ctx, prefix, per)
	if err != nil {
		slog.Warn("autocomplete-multi: users failed", "err", err)
	}
	out = append(out, users...)

	// Hashtags — prefix match on hashtag keyword, sized cap.
	if tags, err := s.autocompleteHashtags(ctx, prefix, per); err != nil {
		slog.Warn("autocomplete-multi: hashtags failed", "err", err)
	} else {
		out = append(out, tags...)
	}

	// Communities — prefix on handle + name.
	if comms, err := s.autocompleteCommunities(ctx, prefix, per); err != nil {
		slog.Warn("autocomplete-multi: communities failed", "err", err)
	} else {
		out = append(out, comms...)
	}

	if len(out) > limit*2 {
		out = out[:limit*2]
	}
	return out, nil
}

func (s *Store) autocompleteHashtags(ctx context.Context, prefix string, limit int) ([]AutocompleteResult, error) {
	p := strings.ToLower(strings.TrimSpace(prefix))
	if len(p) < 1 {
		return nil, nil
	}
	q := map[string]any{
		"size":    limit,
		"timeout": "2s",
		"query": map[string]any{
			"prefix": map[string]any{"hashtag": map[string]any{"value": p}},
		},
		"sort":    []any{map[string]any{"engagement_score": map[string]any{"order": "desc"}}},
		"_source": []string{"hashtag", "use_count"},
	}
	tCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	docs, err := s.execGenericSearch(tCtx, IndexHashtags, q)
	if err != nil {
		return nil, err
	}
	out := make([]AutocompleteResult, 0, len(docs))
	for _, d := range docs {
		h, _ := d["hashtag"].(string)
		if h == "" {
			continue
		}
		out = append(out, AutocompleteResult{Kind: "hashtag", Hashtag: h})
	}
	return out, nil
}

func (s *Store) autocompleteCommunities(ctx context.Context, prefix string, limit int) ([]AutocompleteResult, error) {
	if strings.TrimSpace(prefix) == "" {
		return nil, nil
	}
	q := map[string]any{
		"size":    limit,
		"timeout": "2s",
		"query": map[string]any{
			"bool": map[string]any{
				"should": []any{
					map[string]any{"prefix": map[string]any{"handle": strings.ToLower(prefix)}},
					map[string]any{"match_phrase_prefix": map[string]any{"name": prefix}},
				},
				"minimum_should_match": 1,
			},
		},
		"sort":    []any{map[string]any{"engagement_score": map[string]any{"order": "desc"}}},
		"_source": []string{"community_id", "handle", "name"},
	}
	tCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	docs, err := s.execGenericSearch(tCtx, IndexCommunities, q)
	if err != nil {
		return nil, err
	}
	out := make([]AutocompleteResult, 0, len(docs))
	for _, d := range docs {
		id, _ := d["community_id"].(string)
		out = append(out, AutocompleteResult{
			Kind:        "community",
			CommunityID: id,
			Handle:      toString(d["handle"]),
			Name:        toString(d["name"]),
		})
	}
	return out, nil
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// GetPopularPosts returns posts sorted by like_count descending (for discovery).
//
// Audit CS3: previously `match_all`, which surfaced friends-only and
// circle posts in the public discovery feed. Filter is now constrained
// to visibility=public.
func (s *Store) GetPopularPosts(ctx context.Context, limit int) ([]PostDoc, error) {
	q := map[string]interface{}{
		"size": limit,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"filter": []interface{}{
					map[string]interface{}{"term": map[string]interface{}{"visibility": "public"}},
				},
			},
		},
		"sort": []interface{}{
			map[string]interface{}{
				"like_count": map[string]interface{}{
					"order": "desc",
				},
			},
		},
	}
	return s.execPostSearch(ctx, q)
}

// execGenericSearch runs a search against the given index and returns raw _source maps.
func (s *Store) execGenericSearch(ctx context.Context, index string, query interface{}) ([]map[string]any, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(query); err != nil {
		return nil, err
	}

	res, err := s.client.Search(
		s.client.Search.WithContext(ctx),
		s.client.Search.WithIndex(index),
		s.client.Search.WithBody(&buf),
	)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("search error [%s]: %s", index, res.String())
	}

	var r map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		return nil, err
	}

	hitsOuter, _ := r["hits"].(map[string]interface{})
	hitsInner, _ := hitsOuter["hits"].([]interface{})
	docs := make([]map[string]any, 0, len(hitsInner))
	for _, hit := range hitsInner {
		source, _ := hit.(map[string]interface{})["_source"].(map[string]interface{})
		docs = append(docs, source)
	}
	return docs, nil
}

// IndexProduct indexes a product document into products_v1.
func (s *Store) IndexProduct(ctx context.Context, doc map[string]any) error {
	id, _ := doc["product_id"].(string)
	data, _ := json.Marshal(doc)
	req := opensearchapi.IndexRequest{
		Index:      "products_v1",
		DocumentID: id,
		Body:       bytes.NewReader(data),
	}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("index product error: %s", res.String())
	}
	return nil
}

// IndexEvent indexes an event document into events_v1.
func (s *Store) IndexEvent(ctx context.Context, doc map[string]any) error {
	id, _ := doc["event_id"].(string)
	data, _ := json.Marshal(doc)
	req := opensearchapi.IndexRequest{
		Index:      "events_v1",
		DocumentID: id,
		Body:       bytes.NewReader(data),
	}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("index event error: %s", res.String())
	}
	return nil
}

// IndexMessage indexes a message document into messages_v1.
func (s *Store) IndexMessage(ctx context.Context, doc map[string]any) error {
	id, _ := doc["message_id"].(string)
	data, _ := json.Marshal(doc)
	req := opensearchapi.IndexRequest{
		Index:      "messages_v1",
		DocumentID: id,
		Body:       bytes.NewReader(data),
	}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("index message error: %s", res.String())
	}
	return nil
}

// SearchProducts searches products by query text with optional category filter.
func (s *Store) SearchProducts(ctx context.Context, query, category string, limit int) ([]map[string]any, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	must := []interface{}{
		map[string]interface{}{
			"multi_match": map[string]interface{}{
				"query":  query,
				"fields": []string{"title^2", "description", "category"},
			},
		},
	}
	if category != "" {
		must = append(must, map[string]interface{}{
			"term": map[string]interface{}{"category": category},
		})
	}

	q := map[string]interface{}{
		"size": limit,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": must,
			},
		},
	}
	return s.execGenericSearch(ctx, "products_v1", q)
}

// SearchEvents searches events by query text.
func (s *Store) SearchEvents(ctx context.Context, query string, limit int) ([]map[string]any, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	q := map[string]interface{}{
		"size": limit,
		"query": map[string]interface{}{
			"multi_match": map[string]interface{}{
				"query":  query,
				"fields": []string{"title^2", "description"},
			},
		},
	}
	return s.execGenericSearch(ctx, "events_v1", q)
}

// --- New-entity index/delete methods --------------------------------------

// IndexHashtag upserts a hashtag document keyed by the lowercase tag.
func (s *Store) IndexHashtag(ctx context.Context, doc HashtagDoc) error {
	if doc.HashtagSearch == "" {
		doc.HashtagSearch = doc.Hashtag
	}
	if doc.EngagementScore == 0 {
		doc.EngagementScore = computeEngagementScore(EntityHashtags, engagementCounters{UseCount: doc.UseCount})
	}
	return s.indexDoc(ctx, IndexHashtags, doc.Hashtag, doc)
}

// IndexCommunity upserts a community document keyed by community_id.
func (s *Store) IndexCommunity(ctx context.Context, doc CommunityDoc) error {
	if doc.EngagementScore == 0 {
		doc.EngagementScore = computeEngagementScore(EntityCommunities, engagementCounters{Members: doc.MemberCount})
	}
	return s.indexDoc(ctx, IndexCommunities, doc.CommunityID, doc)
}

// IndexChannel upserts a channel document keyed by channel_id.
func (s *Store) IndexChannel(ctx context.Context, doc ChannelDoc) error {
	if doc.EngagementScore == 0 {
		doc.EngagementScore = computeEngagementScore(EntityChannels, engagementCounters{Subscribers: doc.SubscriberCount})
	}
	return s.indexDoc(ctx, IndexChannels, doc.ChannelID, doc)
}

// IndexProductDoc upserts a typed ProductDoc (the original IndexProduct
// stays for legacy map[string]any callers).
func (s *Store) IndexProductDoc(ctx context.Context, doc ProductDoc) error {
	if doc.EngagementScore == 0 {
		doc.EngagementScore = computeEngagementScore(EntityProducts, engagementCounters{Views: doc.ViewCount, Purchases: doc.OrderCount})
	}
	return s.indexDoc(ctx, IndexProducts, doc.ProductID, doc)
}

// DeleteCommunity / DeleteChannel / DeleteProduct / DeleteHashtag —
// idempotent removals (404 is not an error).
func (s *Store) DeleteCommunity(ctx context.Context, id string) error { return s.deleteDoc(ctx, IndexCommunities, id) }
func (s *Store) DeleteChannel(ctx context.Context, id string) error   { return s.deleteDoc(ctx, IndexChannels, id) }
func (s *Store) DeleteProduct(ctx context.Context, id string) error   { return s.deleteDoc(ctx, IndexProducts, id) }
func (s *Store) DeleteHashtag(ctx context.Context, id string) error   { return s.deleteDoc(ctx, IndexHashtags, id) }

// indexDoc is the shared single-doc index helper.
func (s *Store) indexDoc(ctx context.Context, index, id string, doc any) error {
	if id == "" {
		return fmt.Errorf("indexDoc: empty document id for index %s", index)
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	req := opensearchapi.IndexRequest{
		Index:      index,
		DocumentID: id,
		Body:       bytes.NewReader(data),
	}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("index %s error: %s", index, res.String())
	}
	return nil
}

// deleteDoc is the shared single-doc delete helper. 404 is treated as
// success (the indexer is at-least-once; double-deletes are fine).
func (s *Store) deleteDoc(ctx context.Context, index, id string) error {
	if id == "" {
		return nil
	}
	req := opensearchapi.DeleteRequest{Index: index, DocumentID: id}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() && res.StatusCode != 404 {
		return fmt.Errorf("delete %s error: %s", index, res.String())
	}
	return nil
}

// AddToEngagementScore performs a scripted partial update that adds
// `delta` to engagement_score on the named index. Used by the Kafka
// consumer to apply real-time +1 / -1 / +N adjustments as
// likes/comments/shares/etc fan in.
//
// `delta` may be negative (e.g. reaction removed). If the document
// doesn't exist yet, the upsert path seeds it with engagement_score=delta;
// this keeps the indexer eventually-consistent even if the lifecycle
// event arrives before the *Created event.
func (s *Store) AddToEngagementScore(ctx context.Context, index, docID string, delta float64) error {
	if docID == "" {
		return nil
	}
	body := map[string]any{
		"script": map[string]any{
			"source": "ctx._source.engagement_score = (ctx._source.engagement_score == null ? 0 : ctx._source.engagement_score) + params.delta",
			"params": map[string]any{"delta": delta},
		},
		"upsert": map[string]any{"engagement_score": delta},
	}
	data, _ := json.Marshal(body)
	req := opensearchapi.UpdateRequest{
		Index:      index,
		DocumentID: docID,
		Body:       bytes.NewReader(data),
	}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() && res.StatusCode != 404 {
		return fmt.Errorf("update engagement_score on %s/%s: %s", index, docID, res.String())
	}
	return nil
}

// IncrementHashtagUse increments a hashtag's use_count by 1 and its
// engagement_score by 1 (same formula). Upserts a baseline doc if the
// tag isn't yet known.
func (s *Store) IncrementHashtagUse(ctx context.Context, hashtag string) error {
	hashtag = strings.ToLower(strings.TrimSpace(hashtag))
	if hashtag == "" {
		return nil
	}
	body := map[string]any{
		"script": map[string]any{
			"source": "ctx._source.use_count = (ctx._source.use_count == null ? 0 : ctx._source.use_count) + 1; ctx._source.engagement_score = (ctx._source.engagement_score == null ? 0 : ctx._source.engagement_score) + 1; if (ctx._source.hashtag == null) { ctx._source.hashtag = params.h; ctx._source.hashtag_search = params.h; }",
			"params": map[string]any{"h": hashtag},
		},
		"upsert": map[string]any{
			"hashtag":          hashtag,
			"hashtag_search":   hashtag,
			"use_count":        1,
			"engagement_score": 1,
		},
	}
	data, _ := json.Marshal(body)
	req := opensearchapi.UpdateRequest{
		Index:      IndexHashtags,
		DocumentID: hashtag,
		Body:       bytes.NewReader(data),
	}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("hashtag increment %s: %s", hashtag, res.String())
	}
	return nil
}

// --- Ranked / personalized search -----------------------------------------

// RankedSearchOptions configures a function_score search.
type RankedSearchOptions struct {
	// Limit caps the number of hits returned (default 20, max 100).
	Limit int
	// Cursor: opaque from-offset string. Empty = first page.
	Cursor string
	// FollowedAuthorIDs is the viewer's follow graph slice (up to 500).
	// When non-empty, posts/users authored by these IDs get a 1.5x
	// affinity boost. Empty = anonymous viewer; no boost layered.
	FollowedAuthorIDs []string
}

// buildFunctionScoreQuery returns the OpenSearch query body for a
// ranked search against the given entity index. The shape is:
//
//	function_score {
//	  query:     multi_match across entity-specific fields
//	  functions: [ field_value_factor(engagement_score),
//	               gauss(created_at, 7d),
//	               (optional) filter+weight on follow graph ]
//	  score_mode: multiply
//	  boost_mode: multiply
//	}
//
// Anonymous viewers omit the author-affinity function.
func buildFunctionScoreQuery(entity, q string, opts RankedSearchOptions) map[string]any {
	limit := opts.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	from := 0
	if opts.Cursor != "" {
		if n, err := strconv.Atoi(opts.Cursor); err == nil && n >= 0 && n <= 10000 {
			from = n
		}
	}

	// Per-entity text query — different fields per index.
	var inner map[string]any
	var affinityField string
	filter := []map[string]any{}

	switch entity {
	case EntityPosts:
		inner = map[string]any{
			"multi_match": map[string]any{
				"query":  q,
				"fields": []string{"text^3", "hashtags^2", "author_username"},
			},
		}
		// Always exclude non-public posts (matches SearchPostsFiltered).
		filter = append(filter, map[string]any{"term": map[string]any{"visibility": "public"}})
		affinityField = "author_id"
	case EntityUsers:
		inner = map[string]any{
			"multi_match": map[string]any{
				"query":     q,
				"fields":    []string{"username^4", "display_name^3", "bio"},
				"fuzziness": "AUTO",
			},
		}
		affinityField = "user_id"
	case EntityHashtags:
		inner = map[string]any{
			"multi_match": map[string]any{
				"query":  q,
				"fields": []string{"hashtag^4", "hashtag_search^2"},
			},
		}
	case EntityProducts:
		inner = map[string]any{
			"multi_match": map[string]any{
				"query":  q,
				"fields": []string{"title^3", "description", "category^2"},
			},
		}
		affinityField = "seller_id"
	case EntityCommunities:
		inner = map[string]any{
			"multi_match": map[string]any{
				"query":  q,
				"fields": []string{"name^3", "handle^2", "description", "topic_tags^2", "category"},
			},
		}
		affinityField = "owner_id"
	case EntityChannels:
		inner = map[string]any{
			"multi_match": map[string]any{
				"query":  q,
				"fields": []string{"name^3", "handle^2", "description", "category^2"},
			},
		}
		affinityField = "owner_id"
	default:
		inner = map[string]any{"match_all": map[string]any{}}
	}

	// Wrap inner + filters in a bool so visibility / future filters apply.
	boolQuery := map[string]any{"must": []any{inner}}
	if len(filter) > 0 {
		boolQuery["filter"] = filter
	}

	functions := []map[string]any{
		{
			"field_value_factor": map[string]any{
				"field":    "engagement_score",
				"modifier": "log1p",
				"missing":  0,
			},
		},
		{
			"gauss": map[string]any{
				"created_at": map[string]any{
					"origin": "now",
					"scale":  "7d",
					"decay":  0.5,
				},
			},
		},
	}
	if affinityField != "" && len(opts.FollowedAuthorIDs) > 0 {
		functions = append(functions, map[string]any{
			"filter": map[string]any{
				"terms": map[string]any{affinityField: opts.FollowedAuthorIDs},
			},
			"weight": 1.5,
		})
	}

	return map[string]any{
		"from": from,
		"size": limit,
		"query": map[string]any{
			"function_score": map[string]any{
				"query":      map[string]any{"bool": boolQuery},
				"functions":  functions,
				"score_mode": "multiply",
				"boost_mode": "multiply",
			},
		},
	}
}

// RankedSearchResult is one entity's slice of a multi-entity search.
type RankedSearchResult struct {
	Items      []map[string]any `json:"items"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

// RankedSearch runs a function_score search against the given entity
// index and returns the raw _source maps + an offset-style cursor.
// Cursor format is an integer string; empty when no more results exist.
func (s *Store) RankedSearch(ctx context.Context, entity, q string, opts RankedSearchOptions) (*RankedSearchResult, error) {
	index := EntityToIndex(entity)
	if index == "" {
		return nil, fmt.Errorf("unknown entity type %q", entity)
	}
	query := buildFunctionScoreQuery(entity, q, opts)

	docs, err := s.execGenericSearch(ctx, index, query)
	if err != nil {
		return nil, err
	}
	res := &RankedSearchResult{Items: docs}
	// Cursor: signal "more available" when we filled the page.
	limit := opts.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if len(docs) == limit {
		from := 0
		if opts.Cursor != "" {
			if n, err := strconv.Atoi(opts.Cursor); err == nil {
				from = n
			}
		}
		res.NextCursor = strconv.Itoa(from + limit)
	}
	return res, nil
}

// SearchMessages searches messages within an optional conversation for the given user.
func (s *Store) SearchMessages(ctx context.Context, userID, convID, query string, limit int) ([]map[string]any, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	filters := []interface{}{
		map[string]interface{}{
			"bool": map[string]interface{}{
				"should": []interface{}{
					map[string]interface{}{"term": map[string]interface{}{"sender_id": userID}},
				},
				"minimum_should_match": 1,
			},
		},
	}
	if convID != "" {
		filters = append(filters, map[string]interface{}{
			"term": map[string]interface{}{"conversation_id": convID},
		})
	}

	q := map[string]interface{}{
		"size": limit,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": map[string]interface{}{
					"match": map[string]interface{}{"text": query},
				},
				"filter": filters,
			},
		},
		"sort": []interface{}{
			map[string]interface{}{"ts": map[string]interface{}{"order": "desc"}},
		},
	}
	return s.execGenericSearch(ctx, "messages_v1", q)
}

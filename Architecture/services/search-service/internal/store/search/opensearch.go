package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
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
	UserID        string `json:"user_id"`
	Username      string `json:"username"`
	DisplayName   string `json:"display_name"`
	Bio           string `json:"bio"`
	AvatarMediaID string `json:"avatar_media_id,omitempty"`
	IsVerified    bool   `json:"is_verified"`
}

type PostDoc struct {
	PostID         string    `json:"post_id"`
	AuthorID       string    `json:"author_id"`
	AuthorUsername string    `json:"author_username,omitempty"`
	Text           string    `json:"text"`
	Hashtags       []string  `json:"hashtags,omitempty"`
	Visibility     string    `json:"visibility,omitempty"`
	LikeCount      int       `json:"like_count"`
	CommentCount   int       `json:"comment_count"`
	PostType       string    `json:"post_type,omitempty"`
	AppOrigin      string    `json:"app_origin,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
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

// BulkIndexUsers indexes multiple users in one batch.
func (s *Store) BulkIndexUsers(ctx context.Context, docs []UserDoc) (int, error) {
	if len(docs) == 0 {
		return 0, nil
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
		"size": 0, // No document hits needed, only aggregation results
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

// AutocompleteResult represents a single autocomplete suggestion.
type AutocompleteResult struct {
	UserID      string `json:"user_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

// Autocomplete returns username suggestions for the given prefix.
func (s *Store) Autocomplete(ctx context.Context, prefix string, limit int) ([]AutocompleteResult, error) {
	if limit <= 0 || limit > 20 {
		limit = 10
	}

	query := map[string]interface{}{
		"size": limit,
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
		results = append(results, h.Source)
	}
	return results, nil
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

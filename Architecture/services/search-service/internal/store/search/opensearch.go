package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
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
	// Users Index
	s.createIndexIfNotExists(ctx, "users_v1", `{
		"settings": { "number_of_shards": 1, "number_of_replicas": 0 },
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
		"settings": { "number_of_shards": 1, "number_of_replicas": 0 },
		"mappings": {
			"properties": {
				"post_id": { "type": "keyword" },
				"author_id": { "type": "keyword" },
				"text": { "type": "text" },
				"created_at": { "type": "date" }
			}
		}
	}`)
}

func (s *Store) createIndexIfNotExists(ctx context.Context, index, body string) {
	exists, err := opensearchapi.IndicesExistsRequest{
		Index: []string{index},
	}.Do(ctx, s.client)

	if err != nil {
		log.Printf("Error checking index %s: %v", index, err)
		return
	}
	defer exists.Body.Close()

	if exists.StatusCode == 404 {
		create, err := opensearchapi.IndicesCreateRequest{
			Index: index,
			Body:  strings.NewReader(body),
		}.Do(ctx, s.client)
		if err != nil {
			log.Printf("Error creating index %s: %v", index, err)
			return
		}
		defer create.Body.Close()
		log.Printf("Created index: %s", index)
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
	PostID    string    `json:"post_id"`
	AuthorID  string    `json:"author_id"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

// IndexUser
func (s *Store) IndexUser(ctx context.Context, doc UserDoc) error {
	data, _ := json.Marshal(doc)
	req := opensearchapi.IndexRequest{
		Index:      "users_v1",
		DocumentID: doc.UserID,
		Body:       bytes.NewReader(data),
		Refresh:    "true", // Immediate refresh for dev, remove for prod performance
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
		Refresh:    "true",
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
		s.client.Bulk.WithRefresh("true"),
	)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()

	if res.IsError() {
		return 0, fmt.Errorf("bulk index error: %s", res.String())
	}

	return len(docs), nil
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
	q := map[string]interface{}{
		"size": limit,
		"query": map[string]interface{}{
			"match": map[string]interface{}{
				"text": query,
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

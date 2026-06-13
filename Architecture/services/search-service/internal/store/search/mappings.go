package search

// Index name constants. We use suffixless logical names externally and
// version the physical index via an alias only when a re-mapping is
// needed. For now, the existing v1 indices stay (posts_v1, users_v1,
// products_v1) and the new entities ship as plain hashtags/communities
// /channels indices.
const (
	IndexPosts       = "posts_v1"
	IndexUsers       = "users_v1"
	IndexHashtags    = "hashtags_v1"
	IndexProducts    = "products_v1"
	IndexCommunities = "communities_v1"
	IndexChannels    = "channels_v1"
)

// EntityType is the public-API key clients use to scope a multi-entity
// search. Order here is the order results group into the response.
const (
	EntityPosts       = "posts"
	EntityUsers       = "users"
	EntityHashtags    = "hashtags"
	EntityProducts    = "products"
	EntityCommunities = "communities"
	EntityChannels    = "channels"
)

// AllEntities is the default for `?types=` when the caller omits it.
var AllEntities = []string{
	EntityPosts,
	EntityUsers,
	EntityHashtags,
	EntityProducts,
	EntityCommunities,
	EntityChannels,
}

// EntityToIndex maps a public entity key to its physical OpenSearch
// index name. Returns "" for unknown keys.
func EntityToIndex(entity string) string {
	switch entity {
	case EntityPosts:
		return IndexPosts
	case EntityUsers:
		return IndexUsers
	case EntityHashtags:
		return IndexHashtags
	case EntityProducts:
		return IndexProducts
	case EntityCommunities:
		return IndexCommunities
	case EntityChannels:
		return IndexChannels
	}
	return ""
}

// engagementWeights returns the multiplicative weights applied to the
// raw counters when computing engagement_score per entity. Centralized
// here so the Kafka consumer + backfill use the exact same formula.
type engagementCounters struct {
	// posts: like + 2*comment + 3*share + bookmark
	Likes     int
	Comments  int
	Shares    int
	Bookmarks int
	// users: follower_count + 0.5*post_count
	Followers int
	Posts     int
	// hashtags: use_count
	UseCount int
	// products: view_count + 5*purchase_count (we use order_count as a
	// proxy for purchase_count since the products table has no
	// purchase_count column today)
	Views     int
	Purchases int
	// communities: member_count
	Members int
	// channels: subscriber_count
	Subscribers int
}

// computeEngagementScore returns the engagement score for a given
// entity using the canonical formula. Returns 0 for unknown entity
// types so an indexer doesn't blow up on a future entity we forgot
// to wire here.
func computeEngagementScore(entity string, c engagementCounters) float64 {
	switch entity {
	case EntityPosts:
		return float64(c.Likes + 2*c.Comments + 3*c.Shares + c.Bookmarks)
	case EntityUsers:
		return float64(c.Followers) + 0.5*float64(c.Posts)
	case EntityHashtags:
		return float64(c.UseCount)
	case EntityProducts:
		return float64(c.Views + 5*c.Purchases)
	case EntityCommunities:
		return float64(c.Members)
	case EntityChannels:
		return float64(c.Subscribers)
	}
	return 0
}

// initEntityIndices is called from initIndices() to register the four
// new indices (hashtags, communities, channels — products/users/posts
// keep their existing mappings + are extended with engagement_score
// in IndexPost / IndexUser doc structs).
//
// All new indices ship with engagement_score (double) and created_at
// (date) so the function_score query has somewhere to read from on
// docs that pre-date a counter update.
func (s *Store) initEntityIndices() {
	ctx := newBgCtx()
	settings := opensearchSettingsJSON()

	// Extend users_v1 / posts_v1 / products_v1 with engagement_score
	// and (for posts) created_at — these are idempotent put_mapping
	// calls so re-runs are safe. The original CreateIndex bodies in
	// initIndices() didn't carry engagement_score; we patch it in
	// here without rewriting the originals so the diff stays small.
	s.putEngagementMapping(ctx, IndexUsers)
	s.putEngagementMapping(ctx, IndexPosts)
	s.putEngagementMapping(ctx, IndexProducts)

	// Hashtags index — one doc per hashtag, keyed by the lowercase tag.
	s.createIndexIfNotExists(ctx, IndexHashtags, `{
		"settings": `+settings+`,
		"mappings": {
			"properties": {
				"hashtag":          { "type": "keyword" },
				"hashtag_search":   { "type": "text", "analyzer": "standard" },
				"use_count":        { "type": "long" },
				"engagement_score": { "type": "double" },
				"created_at":       { "type": "date" }
			}
		}
	}`)

	// Communities index.
	s.createIndexIfNotExists(ctx, IndexCommunities, `{
		"settings": `+settings+`,
		"mappings": {
			"properties": {
				"community_id":     { "type": "keyword" },
				"owner_id":         { "type": "keyword" },
				"handle":           { "type": "keyword" },
				"name":             { "type": "text", "fields": { "keyword": { "type": "keyword" } } },
				"description":      { "type": "text" },
				"community_type":   { "type": "keyword" },
				"category":         { "type": "keyword" },
				"topic_tags":       { "type": "keyword" },
				"member_count":     { "type": "long" },
				"is_verified":      { "type": "boolean" },
				"engagement_score": { "type": "double" },
				"created_at":       { "type": "date" }
			}
		}
	}`)

	// Channels index.
	s.createIndexIfNotExists(ctx, IndexChannels, `{
		"settings": `+settings+`,
		"mappings": {
			"properties": {
				"channel_id":       { "type": "keyword" },
				"owner_id":         { "type": "keyword" },
				"handle":           { "type": "keyword" },
				"name":             { "type": "text", "fields": { "keyword": { "type": "keyword" } } },
				"description":      { "type": "text" },
				"channel_type":     { "type": "keyword" },
				"category":         { "type": "keyword" },
				"subscriber_count": { "type": "long" },
				"is_verified":      { "type": "boolean" },
				"engagement_score": { "type": "double" },
				"created_at":       { "type": "date" }
			}
		}
	}`)
}

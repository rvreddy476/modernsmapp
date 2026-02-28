package engagement

import (
	"encoding/binary"

	"github.com/google/uuid"
)

// LIKE_SHARDS is the number of shards for post_likes and post_shares tables.
// Sharding prevents hot partitions on viral posts by distributing writes across
// 16 partitions per post. The shard is deterministic from user_id, so membership
// checks ("did I like this?") still target exactly one partition — no scatter-gather.
const LIKE_SHARDS = 16

// LikeShard returns the shard index (0..15) for a user's like on a post.
func LikeShard(userID uuid.UUID) int16 {
	h := binary.BigEndian.Uint64(userID[:8])
	return int16(h % LIKE_SHARDS)
}

// ShareShard returns the shard index (0..15) for a user's share on a post.
// Uses the same algorithm as LikeShard.
func ShareShard(userID uuid.UUID) int16 {
	h := binary.BigEndian.Uint64(userID[:8])
	return int16(h % LIKE_SHARDS)
}

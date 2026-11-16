package engagement

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// LuaToggleResult holds the result from an atomic Lua toggle script.
type LuaToggleResult struct {
	IsSet    bool  // true = created, false = removed
	Count    int64 // updated counter value
	Seq      int64 // user's monotonic sequence number
	ActionTS int64 // microsecond timestamp captured in the script
}

// CommentReactionResult holds the result from a comment like/dislike toggle with mutual exclusion.
type CommentReactionResult struct {
	IsSet           bool  // true = created, false = removed
	LikeCount       int64 // updated like counter
	DislikeCount    int64 // updated dislike counter
	Seq             int64
	ActionTS        int64
	OppositeRemoved bool // true if the opposite reaction was removed (e.g. dislike removed when liking)
}

// likeSetScript records a like that was SET or REMOVED in the Redis copies,
// in one round trip:
//   - INCR per-user sequence counter
//   - membership key (SET with TTL / DEL)
//   - HINCRBY engagement counter (+1 / -1, never below zero)
//   - SADD / SREM liker set (shared with feed-service)
//
// It does not decide anything (feed-49). It used to be a toggle that flipped
// on EXISTS of the membership key, and that key expires after 24 h: a tap
// meant to unlike, a day later, ran the "like" branch and counted the viewer
// again here, in engagement_counters and in PostgreSQL like_count. The
// stored reaction (Scylla reactions_by_post, written IF NOT EXISTS / IF
// EXISTS) decides now, and the caller runs this only when that write changed
// the row, so one like moves each counter once. The membership key is a
// cache of that answer and may expire freely.
//
// KEYS[1] = liked:{uid}:{pid}     (membership)
// KEYS[2] = post:eng:{pid}        (counter hash)
// KEYS[3] = post:likers:{pid}     (liker set)
// KEYS[4] = eng:seq:{uid}         (sequence counter)
// ARGV[1] = userID string
// ARGV[2] = nowMicro (microsecond timestamp)
// ARGV[3] = "1" the like was set, "0" it was removed
//
// Returns: {is_set(0/1), count, seq, nowMicro}
var likeSetScript = redis.NewScript(`
	local likeKey   = KEYS[1]
	local engKey    = KEYS[2]
	local likersKey = KEYS[3]
	local seqKey    = KEYS[4]
	local userID    = ARGV[1]
	local nowMicro  = tonumber(ARGV[2])

	local seq = redis.call('INCR', seqKey)
	redis.call('EXPIRE', seqKey, 86400)

	if ARGV[3] == '1' then
		redis.call('SET', likeKey, '1', 'EX', 86400)
		local count = redis.call('HINCRBY', engKey, 'likes', 1)
		redis.call('SADD', likersKey, userID)
		return {1, count, seq, nowMicro}
	else
		redis.call('DEL', likeKey)
		local count = redis.call('HINCRBY', engKey, 'likes', -1)
		if count < 0 then
			-- a like recorded by a route that does not count here
			redis.call('HSET', engKey, 'likes', 0)
			count = 0
		end
		redis.call('SREM', likersKey, userID)
		return {0, count, seq, nowMicro}
	end
`)

// bookmarkToggleScript atomically toggles a bookmark.
// NO liker list, NO notification — bookmarks are completely private.
//
// KEYS[1] = bookmarked:{uid}:{pid}  (membership with 24h TTL)
// KEYS[2] = eng:seq:{uid}           (sequence counter)
// ARGV[1] = nowMicro
//
// Returns: {is_set(0/1), seq, nowMicro}
var bookmarkToggleScript = redis.NewScript(`
	local bmKey   = KEYS[1]
	local seqKey  = KEYS[2]
	local nowMicro = tonumber(ARGV[1])

	local seq = redis.call('INCR', seqKey)
	redis.call('EXPIRE', seqKey, 86400)

	local exists = redis.call('EXISTS', bmKey)
	if exists == 1 then
		redis.call('DEL', bmKey)
		return {0, seq, nowMicro}
	else
		redis.call('SET', bmKey, '1', 'EX', 86400)
		return {1, seq, nowMicro}
	end
`)

// commentLikeToggleScript atomically toggles a comment like with mutual exclusion.
// If setting a like and a dislike exists, the dislike is removed first.
//
// KEYS[1] = comment_liked:{uid}:{cid}     (like membership)
// KEYS[2] = comment_disliked:{uid}:{cid}  (dislike membership)
// KEYS[3] = comment:eng:{cid}             (counter hash)
// KEYS[4] = eng:seq:{uid}                 (sequence counter)
// ARGV[1] = nowMicro
//
// Returns: {is_set(0/1), like_count, dislike_count, seq, nowMicro, opposite_removed(0/1)}
var commentLikeToggleScript = redis.NewScript(`
	local likeKey    = KEYS[1]
	local dislikeKey = KEYS[2]
	local engKey     = KEYS[3]
	local seqKey     = KEYS[4]
	local nowMicro   = tonumber(ARGV[1])

	local seq = redis.call('INCR', seqKey)
	redis.call('EXPIRE', seqKey, 86400)

	local exists = redis.call('EXISTS', likeKey)
	local oppositeRemoved = 0

	if exists == 1 then
		redis.call('DEL', likeKey)
		redis.call('HINCRBY', engKey, 'likes', -1)
		local lc = redis.call('HGET', engKey, 'likes')
		local dc = redis.call('HGET', engKey, 'dislikes')
		return {0, tonumber(lc) or 0, tonumber(dc) or 0, seq, nowMicro, 0}
	else
		-- Setting like: remove dislike if present
		if redis.call('EXISTS', dislikeKey) == 1 then
			redis.call('DEL', dislikeKey)
			redis.call('HINCRBY', engKey, 'dislikes', -1)
			oppositeRemoved = 1
		end
		redis.call('SET', likeKey, '1', 'EX', 86400)
		redis.call('HINCRBY', engKey, 'likes', 1)
		local lc = redis.call('HGET', engKey, 'likes')
		local dc = redis.call('HGET', engKey, 'dislikes')
		return {1, tonumber(lc) or 0, tonumber(dc) or 0, seq, nowMicro, oppositeRemoved}
	end
`)

// commentDislikeToggleScript atomically toggles a comment dislike with mutual exclusion.
// If setting a dislike and a like exists, the like is removed first.
//
// KEYS[1] = comment_disliked:{uid}:{cid}  (dislike membership)
// KEYS[2] = comment_liked:{uid}:{cid}     (like membership)
// KEYS[3] = comment:eng:{cid}             (counter hash)
// KEYS[4] = eng:seq:{uid}                 (sequence counter)
// ARGV[1] = nowMicro
//
// Returns: {is_set(0/1), dislike_count, like_count, seq, nowMicro, opposite_removed(0/1)}
var commentDislikeToggleScript = redis.NewScript(`
	local dislikeKey = KEYS[1]
	local likeKey    = KEYS[2]
	local engKey     = KEYS[3]
	local seqKey     = KEYS[4]
	local nowMicro   = tonumber(ARGV[1])

	local seq = redis.call('INCR', seqKey)
	redis.call('EXPIRE', seqKey, 86400)

	local exists = redis.call('EXISTS', dislikeKey)
	local oppositeRemoved = 0

	if exists == 1 then
		redis.call('DEL', dislikeKey)
		redis.call('HINCRBY', engKey, 'dislikes', -1)
		local dc = redis.call('HGET', engKey, 'dislikes')
		local lc = redis.call('HGET', engKey, 'likes')
		return {0, tonumber(dc) or 0, tonumber(lc) or 0, seq, nowMicro, 0}
	else
		-- Setting dislike: remove like if present
		if redis.call('EXISTS', likeKey) == 1 then
			redis.call('DEL', likeKey)
			redis.call('HINCRBY', engKey, 'likes', -1)
			oppositeRemoved = 1
		end
		redis.call('SET', dislikeKey, '1', 'EX', 86400)
		redis.call('HINCRBY', engKey, 'dislikes', 1)
		local dc = redis.call('HGET', engKey, 'dislikes')
		local lc = redis.call('HGET', engKey, 'likes')
		return {1, tonumber(dc) or 0, tonumber(lc) or 0, seq, nowMicro, oppositeRemoved}
	end
`)

// SetLike records in Redis that the viewer's like on a post was set (set =
// true) or removed, and returns the counter after it. Call it only when the
// stored reaction changed (likeSetScript): it counts every call.
func SetLike(ctx context.Context, rdb *redis.Client, userID uuid.UUID, postID uuid.UUID, set bool) (*LuaToggleResult, error) {
	uid := userID.String()
	pid := postID.String()
	nowMicro := time.Now().UnixMicro()

	keys := []string{
		fmt.Sprintf("liked:%s:%s", uid, pid),
		fmt.Sprintf("post:eng:%s", pid),
		fmt.Sprintf("post:likers:%s", pid),
		fmt.Sprintf("eng:seq:%s", uid),
	}
	flag := "0"
	if set {
		flag = "1"
	}

	res, err := likeSetScript.Run(ctx, rdb, keys, uid, nowMicro, flag).Int64Slice()
	if err != nil {
		return nil, fmt.Errorf("like set lua: %w", err)
	}

	return &LuaToggleResult{
		IsSet:    res[0] == 1,
		Count:    res[1],
		Seq:      res[2],
		ActionTS: res[3],
	}, nil
}

// ToggleBookmark executes the atomic bookmark toggle Lua script.
func ToggleBookmark(ctx context.Context, rdb *redis.Client, userID uuid.UUID, postID uuid.UUID) (*LuaToggleResult, error) {
	uid := userID.String()
	pid := postID.String()
	nowMicro := time.Now().UnixMicro()

	keys := []string{
		fmt.Sprintf("bookmarked:%s:%s", uid, pid),
		fmt.Sprintf("eng:seq:%s", uid),
	}

	res, err := bookmarkToggleScript.Run(ctx, rdb, keys, nowMicro).Int64Slice()
	if err != nil {
		return nil, fmt.Errorf("bookmark toggle lua: %w", err)
	}

	return &LuaToggleResult{
		IsSet:    res[0] == 1,
		Count:    0, // bookmarks don't expose count
		Seq:      res[1],
		ActionTS: res[2],
	}, nil
}

// ToggleCommentLike executes the atomic comment-like toggle Lua script with mutual exclusion.
func ToggleCommentLike(ctx context.Context, rdb *redis.Client, userID uuid.UUID, commentID uuid.UUID) (*CommentReactionResult, error) {
	uid := userID.String()
	cid := commentID.String()
	nowMicro := time.Now().UnixMicro()

	keys := []string{
		fmt.Sprintf("comment_liked:%s:%s", uid, cid),
		fmt.Sprintf("comment_disliked:%s:%s", uid, cid),
		fmt.Sprintf("comment:eng:%s", cid),
		fmt.Sprintf("eng:seq:%s", uid),
	}

	res, err := commentLikeToggleScript.Run(ctx, rdb, keys, nowMicro).Int64Slice()
	if err != nil {
		return nil, fmt.Errorf("comment like toggle lua: %w", err)
	}

	return &CommentReactionResult{
		IsSet:           res[0] == 1,
		LikeCount:       res[1],
		DislikeCount:    res[2],
		Seq:             res[3],
		ActionTS:        res[4],
		OppositeRemoved: res[5] == 1,
	}, nil
}

// ToggleCommentDislike executes the atomic comment-dislike toggle Lua script with mutual exclusion.
func ToggleCommentDislike(ctx context.Context, rdb *redis.Client, userID uuid.UUID, commentID uuid.UUID) (*CommentReactionResult, error) {
	uid := userID.String()
	cid := commentID.String()
	nowMicro := time.Now().UnixMicro()

	keys := []string{
		fmt.Sprintf("comment_disliked:%s:%s", uid, cid),
		fmt.Sprintf("comment_liked:%s:%s", uid, cid),
		fmt.Sprintf("comment:eng:%s", cid),
		fmt.Sprintf("eng:seq:%s", uid),
	}

	res, err := commentDislikeToggleScript.Run(ctx, rdb, keys, nowMicro).Int64Slice()
	if err != nil {
		return nil, fmt.Errorf("comment dislike toggle lua: %w", err)
	}

	return &CommentReactionResult{
		IsSet:           res[0] == 1,
		DislikeCount:    res[1],
		LikeCount:       res[2],
		Seq:             res[3],
		ActionTS:        res[4],
		OppositeRemoved: res[5] == 1,
	}, nil
}

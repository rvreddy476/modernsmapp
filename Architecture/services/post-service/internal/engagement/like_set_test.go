package engagement

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// feed-14 / feed-49: the Redis copies of a like (SetLike), run against
// miniredis so the Lua script itself executes.

func newLikeRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, rdb
}

func likesInRedis(t *testing.T, mr *miniredis.Miniredis, post uuid.UUID) string {
	t.Helper()
	return mr.HGet(fmt.Sprintf("post:eng:%s", post), "likes")
}

func TestSetLike_SetThenUnset(t *testing.T) {
	mr, rdb := newLikeRedis(t)
	ctx := context.Background()
	user, post := uuid.New(), uuid.New()
	likedKey := fmt.Sprintf("liked:%s:%s", user, post)
	likersKey := fmt.Sprintf("post:likers:%s", post)

	set, err := SetLike(ctx, rdb, user, post, true)
	if err != nil {
		t.Fatal(err)
	}
	if !set.IsSet || set.Count != 1 || set.Seq != 1 || set.ActionTS == 0 {
		t.Fatalf("set = %+v, want is_set, count 1, seq 1 and a timestamp", set)
	}
	if !mr.Exists(likedKey) || mr.TTL(likedKey) != 24*time.Hour {
		t.Fatalf("membership key: exists=%v ttl=%v, want a 24 h cache entry", mr.Exists(likedKey), mr.TTL(likedKey))
	}
	if ok, _ := mr.SIsMember(likersKey, user.String()); !ok {
		t.Fatal("the viewer is not in the post's liker set after a like")
	}
	if got := likesInRedis(t, mr, post); got != "1" {
		t.Fatalf("likes = %q, want 1", got)
	}

	unset, err := SetLike(ctx, rdb, user, post, false)
	if err != nil {
		t.Fatal(err)
	}
	if unset.IsSet || unset.Count != 0 || unset.Seq != 2 {
		t.Fatalf("unset = %+v, want not set, count 0, seq 2", unset)
	}
	if mr.Exists(likedKey) {
		t.Fatal("membership key survived the unlike")
	}
	if ok, _ := mr.SIsMember(likersKey, user.String()); ok {
		t.Fatal("the viewer is still in the liker set after the unlike")
	}
	if got := likesInRedis(t, mr, post); got != "0" {
		t.Fatalf("likes = %q, want 0", got)
	}
}

// feed-49: the script does what it is told and does not decide from the
// membership key. With the key expired (24 h), removing the like still takes
// one off; the old toggle ran its "like" branch here and counted the viewer
// a second time.
func TestSetLike_DoesNotDecideFromTheExpiredMembershipKey(t *testing.T) {
	mr, rdb := newLikeRedis(t)
	ctx := context.Background()
	user, post := uuid.New(), uuid.New()

	if _, err := SetLike(ctx, rdb, user, post, true); err != nil {
		t.Fatal(err)
	}
	mr.FastForward(25 * time.Hour)
	if mr.Exists(fmt.Sprintf("liked:%s:%s", user, post)) {
		t.Fatal("precondition: the membership key should have expired")
	}

	res, err := SetLike(ctx, rdb, user, post, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsSet || res.Count != 0 {
		t.Fatalf("unlike after the key expired = %+v, want not set and count 0", res)
	}
	if got := likesInRedis(t, mr, post); got != "0" {
		t.Fatalf("likes = %q after like, 25 h, unlike; want 0", got)
	}
}

// A like made through a route that does not count in Redis (POST /reactions)
// and removed through this one leaves the counter at zero, never below.
func TestSetLike_UnsetNeverGoesBelowZero(t *testing.T) {
	mr, rdb := newLikeRedis(t)
	user, post := uuid.New(), uuid.New()

	res, err := SetLike(context.Background(), rdb, user, post, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Count != 0 {
		t.Fatalf("count = %d, want 0", res.Count)
	}
	if got := likesInRedis(t, mr, post); got != "0" {
		t.Fatalf("likes = %q, want 0", got)
	}
}

// Two viewers are two likes; each keeps its own membership key.
func TestSetLike_TwoViewers(t *testing.T) {
	mr, rdb := newLikeRedis(t)
	ctx := context.Background()
	post, a, b := uuid.New(), uuid.New(), uuid.New()
	for _, u := range []uuid.UUID{a, b} {
		if _, err := SetLike(ctx, rdb, u, post, true); err != nil {
			t.Fatal(err)
		}
	}
	if got := likesInRedis(t, mr, post); got != "2" {
		t.Fatalf("likes = %q, want 2", got)
	}
	if _, err := SetLike(ctx, rdb, a, post, false); err != nil {
		t.Fatal(err)
	}
	if ok, _ := mr.SIsMember(fmt.Sprintf("post:likers:%s", post), b.String()); !ok {
		t.Fatal("removing one viewer's like removed the other from the liker set")
	}
	if got := likesInRedis(t, mr, post); got != "1" {
		t.Fatalf("likes = %q, want 1", got)
	}
}

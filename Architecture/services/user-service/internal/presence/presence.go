package presence

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	presenceTTL    = 90 * time.Second // 90s TTL; heartbeat every 30s keeps it alive
	presencePrefix = "presence:"
)

type Store struct {
	rdb *redis.Client
}

func New(rdb *redis.Client) *Store {
	return &Store{rdb: rdb}
}

// SetOnline marks userID as online with a 90-second TTL.
func (s *Store) SetOnline(ctx context.Context, userID string) error {
	key := fmt.Sprintf("%s%s", presencePrefix, userID)
	return s.rdb.Set(ctx, key, "1", presenceTTL).Err()
}

// SetOffline removes the presence key immediately.
func (s *Store) SetOffline(ctx context.Context, userID string) error {
	key := fmt.Sprintf("%s%s", presencePrefix, userID)
	return s.rdb.Del(ctx, key).Err()
}

// IsOnline returns true if the user has a presence key in Redis.
func (s *Store) IsOnline(ctx context.Context, userID string) (bool, error) {
	key := fmt.Sprintf("%s%s", presencePrefix, userID)
	n, err := s.rdb.Exists(ctx, key).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

package service

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"time"

	"github.com/atpost/feature-flag-service/internal/store/postgres"
	"github.com/redis/go-redis/v9"
)

type Evaluator struct {
	store *postgres.Store
	redis *redis.Client
}

func New(store *postgres.Store, rdb *redis.Client) *Evaluator {
	return &Evaluator{store: store, redis: rdb}
}

type EvalResult struct {
	Enabled bool            `json:"enabled"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

func (e *Evaluator) Evaluate(ctx context.Context, key string, userID string) (*EvalResult, error) {
	// 1. Try Redis
	cacheKey := "flag:" + key
	val, err := e.redis.Get(ctx, cacheKey).Result()
	var flag *postgres.Flag

	if err == nil {
		json.Unmarshal([]byte(val), &flag)
	} else {
		// 2. DB Fallback
		flag, err = e.store.GetFlag(ctx, key)
		if err != nil {
			return nil, err
		}
		if flag == nil {
			// Flag doesn't exist, default false
			return &EvalResult{Enabled: false}, nil
		}
		// Cache it
		fBytes, _ := json.Marshal(flag)
		e.redis.Set(ctx, cacheKey, fBytes, 60*time.Second)
	}

	// 3. Evaluate
	if flag == nil {
		return &EvalResult{Enabled: false}, nil
	}

	if !flag.Enabled {
		return &EvalResult{Enabled: false}, nil
	}

	// 4. White list check
	for _, target := range flag.TargetUserIDs {
		if target == userID {
			return &EvalResult{Enabled: true, Payload: flag.Payload}, nil
		}
	}

	// 5. Rollout check (only if not specifically targeted)
	if flag.RolloutPct > 0 {
		hash := fnv.New32a()
		hash.Write([]byte(userID + key))
		score := hash.Sum32() % 100
		if score < uint32(flag.RolloutPct) {
			return &EvalResult{Enabled: true, Payload: flag.Payload}, nil
		}
	}

	return &EvalResult{Enabled: false}, nil
}

func (e *Evaluator) UpsertFlag(ctx context.Context, flag *postgres.Flag) error {
	if err := e.store.UpsertFlag(ctx, flag); err != nil {
		return err
	}
	// Invalidate cache
	e.redis.Del(ctx, "flag:"+flag.Key)
	return nil
}

func (e *Evaluator) ListFlags(ctx context.Context) ([]postgres.Flag, error) {
	return e.store.ListFlags(ctx)
}

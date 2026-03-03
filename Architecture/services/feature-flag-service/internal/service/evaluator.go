package service

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"time"

	"github.com/atpost/feature-flag-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

type Evaluator struct {
	store       *postgres.Store
	redis       *redis.Client
	kafkaWriter *kafka.Writer
}

func New(store *postgres.Store, rdb *redis.Client, kafkaWriter *kafka.Writer) *Evaluator {
	return &Evaluator{store: store, redis: rdb, kafkaWriter: kafkaWriter}
}

type EvalResult struct {
	Enabled bool            `json:"enabled"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

func (e *Evaluator) Evaluate(ctx context.Context, key string, userID string) (*EvalResult, error) {
	result, err := e.evaluate(ctx, key, userID)
	if err != nil {
		return nil, err
	}

	// Emit flag.evaluated event non-blocking, errors suppressed.
	if e.kafkaWriter != nil {
		payload := events.FlagEvaluatedPayload{
			FlagKey: key,
			UserID:  userID,
			Enabled: result.Enabled,
		}
		pBytes, _ := json.Marshal(payload)
		actor := userID
		envelope := events.NewEnvelope(ctx, events.EventFlagEvaluated, &actor, pBytes)
		eBytes, _ := json.Marshal(envelope)
		_ = e.kafkaWriter.WriteMessages(ctx, kafka.Message{
			Key:   []byte(key + ":" + userID),
			Value: eBytes,
		})
	}

	return result, nil
}

func (e *Evaluator) evaluate(ctx context.Context, key string, userID string) (*EvalResult, error) {
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

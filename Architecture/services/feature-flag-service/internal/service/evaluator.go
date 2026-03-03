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
	Variant string          `json:"variant,omitempty"`
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

	// 4. Check experiment dates
	now := time.Now()
	if flag.StartDate != nil && now.Before(*flag.StartDate) {
		return &EvalResult{Enabled: false}, nil // experiment not started
	}
	if flag.EndDate != nil && now.After(*flag.EndDate) {
		return &EvalResult{Enabled: false}, nil // experiment ended
	}

	// 5. White list check
	for _, target := range flag.TargetUserIDs {
		if target == userID {
			return &EvalResult{Enabled: true, Payload: flag.Payload}, nil
		}
	}

	// 6. Rollout check (only if not specifically targeted)
	if flag.RolloutPct > 0 {
		hash := fnv.New32a()
		hash.Write([]byte(userID + key))
		score := hash.Sum32() % 100
		if score < uint32(flag.RolloutPct) {
			// 7. Assign variant deterministically using hash
			variant := ""
			if flag.ControlGroupPct > 0 || flag.TreatmentGroupPct > 0 {
				h := fnv.New32a()
				h.Write([]byte(userID + ":" + flag.Key + ":variant"))
				bucket := int(h.Sum32() % 100)
				if bucket < flag.ControlGroupPct {
					variant = "control"
				} else if bucket < flag.ControlGroupPct+flag.TreatmentGroupPct {
					variant = "treatment"
				}
			}
			return &EvalResult{Enabled: true, Payload: flag.Payload, Variant: variant}, nil
		}
	}

	return &EvalResult{Enabled: false}, nil
}

func (e *Evaluator) UpsertFlag(ctx context.Context, flag *postgres.Flag) error {
	// Get current value for audit log
	oldFlag, _ := e.store.GetFlag(ctx, flag.Key)

	// Existing upsert logic
	if err := e.store.UpsertFlag(ctx, flag); err != nil {
		return err
	}

	// Invalidate Redis cache
	e.redis.Del(ctx, "flag:"+flag.Key)

	// Insert audit log entry
	actor := ctx.Value("actor_user_id") // may be empty; fallback to "system"
	actorStr, _ := actor.(string)
	if actorStr == "" {
		actorStr = "system"
	}
	action := "updated"
	if oldFlag == nil {
		action = "created"
	}

	oldJSON, _ := json.Marshal(oldFlag)
	newJSON, _ := json.Marshal(flag)
	// Best-effort; ignore error so upsert success is not affected by audit log failure
	_ = e.store.InsertAuditLog(ctx, postgres.FlagAuditEntry{
		FlagKey:  flag.Key,
		Actor:    actorStr,
		Action:   action,
		OldValue: oldJSON,
		NewValue: newJSON,
	})

	return nil
}

func (e *Evaluator) ListFlags(ctx context.Context) ([]postgres.Flag, error) {
	return e.store.ListFlags(ctx)
}

// GetAuditLog returns paginated audit log entries for the given flag key.
func (e *Evaluator) GetAuditLog(ctx context.Context, flagKey string, limit, offset int) ([]postgres.FlagAuditEntry, error) {
	return e.store.GetAuditLog(ctx, flagKey, limit, offset)
}

// RecordConversion records an A/B experiment conversion event.
func (e *Evaluator) RecordConversion(ctx context.Context, flagKey, userID, variant, eventType string) error {
	return e.store.InsertConversion(ctx, flagKey, userID, variant, eventType)
}

// GetExperimentResults returns aggregated A/B experiment results for a given flag.
func (e *Evaluator) GetExperimentResults(ctx context.Context, flagKey string) (map[string]interface{}, error) {
	evalCount, _ := e.store.CountEvaluations(ctx, flagKey)
	conversionsByVariant, _ := e.store.CountConversionsByVariant(ctx, flagKey)
	return map[string]interface{}{
		"flag_key":               flagKey,
		"total_evaluations":      evalCount,
		"conversions_by_variant": conversionsByVariant,
	}, nil
}

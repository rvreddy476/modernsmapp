package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/atpost/dating-service/internal/moderation"
	"github.com/google/uuid"
)

// Mechanic M13: the hourly cap on kind checks (needs REDIS_ADDR as well as
// TEST_PG_DSN) and the LLM's part.

func TestKindCheckHourlyLimit(t *testing.T) {
	svc, _, _ := newD3Svc(t)
	if svc.rdb == nil {
		t.Skip("REDIS_ADDR not set; skipping the kind-check limit test")
	}
	svc.SetMechanicsConfig(MechanicsConfig{KindCheck: true})
	ctx := context.Background()
	user := uuid.New()
	key := fmt.Sprintf("dating:kindcheck:%s:%s", user, time.Now().UTC().Format("2006010215"))
	if err := svc.rdb.Set(ctx, key, kindCheckHourlyLimit, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svc.rdb.Del(context.Background(), key) })
	if _, err := svc.KindCheck(ctx, user, "hello"); !errors.Is(err, ErrKindCheckRateLimited) {
		t.Fatalf("over the cap: %v, want the rate limit", err)
	}
}

// scoreLLM answers every text with score.
type scoreLLM struct{ score float64 }

func (l scoreLLM) Score(context.Context, string) (*moderation.LLMResponse, error) {
	return &moderation.LLMResponse{Confidence: l.score}, nil
}

func TestKindCheckUsesARealModel(t *testing.T) {
	svc := New(nil, nil)
	svc.SetMechanicsConfig(MechanicsConfig{KindCheck: true})
	ctx := context.Background()
	svc.SetModerationLLMClient(scoreLLM{score: 0.9})
	res, err := svc.KindCheck(ctx, uuid.New(), "a perfectly plain sentence")
	if err != nil || res.Kind || len(res.Reasons) != 1 || res.Reasons[0] != "tone" {
		t.Fatalf("a model scoring 0.9: %+v %v, want unkind for tone", res, err)
	}
	svc.SetModerationLLMClient(scoreLLM{score: 0.2})
	if res, _ := svc.KindCheck(ctx, uuid.New(), "a perfectly plain sentence"); !res.Kind {
		t.Fatalf("a model scoring 0.2 flagged it: %+v", res)
	}
	svc.SetModerationLLMClient(moderation.NewMockClient())
	if res, _ := svc.KindCheck(ctx, uuid.New(), "my number is 9876543210"); !res.Kind {
		t.Fatalf("the dev mock flagged a phone number: %+v", res)
	}
}

package kafka

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

func TestPermanentClassification(t *testing.T) {
	base := errors.New("bad input")
	if isPermanent(base) {
		t.Fatal("ordinary dependency error classified permanent")
	}
	if !isPermanent(Permanent(base)) {
		t.Fatal("marked poison was not classified permanent")
	}
}

func TestRetryDurableBlocksUntilSuccess(t *testing.T) {
	var calls atomic.Int32
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !retryDurable(ctx, slog.Default(), "test", func() error {
		if calls.Add(1) < 3 {
			return errors.New("transient")
		}
		return nil
	}) {
		t.Fatal("durable retry stopped before success")
	}
	if calls.Load() != 3 {
		t.Fatalf("calls=%d want 3", calls.Load())
	}
}

func TestRetryDurableCancellationDoesNotClaimSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if retryDurable(ctx, slog.Default(), "test", func() error { return errors.New("still down") }) {
		t.Fatal("cancelled durable operation claimed success")
	}
}

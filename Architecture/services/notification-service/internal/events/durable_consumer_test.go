package events

import (
	"context"
	"errors"
	"github.com/segmentio/kafka-go"
	"testing"
)

type commitProbe struct{ calls int }

func (p *commitProbe) CommitMessages(context.Context, ...kafka.Message) error { p.calls++; return nil }
func TestQueueFailureCannotCommitKafkaOffset(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &commitProbe{}
	ok := processAndCommit(ctx, p, kafka.Message{}, func(context.Context, kafka.Message) error { cancel(); return errors.New("database offline") })
	if ok || p.calls != 0 {
		t.Fatalf("failed queue was acknowledged: ok=%v commits=%d", ok, p.calls)
	}
}
func TestPersistedQueueCommitsOnce(t *testing.T) {
	p := &commitProbe{}
	if !processAndCommit(context.Background(), p, kafka.Message{}, func(context.Context, kafka.Message) error { return nil }) || p.calls != 1 {
		t.Fatal("durable handoff not committed")
	}
}

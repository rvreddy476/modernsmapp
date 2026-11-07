package events

import (
	"testing"

	"github.com/segmentio/kafka-go"
)

// Lifecycle events are keyed by channel id so they stay in order per channel;
// only a key-respecting balancer makes that true.
func TestLifecycleWriterPartitionsByKey(t *testing.T) {
	p := NewProducer([]string{"localhost:9092"}, "channel-events", nil)
	defer p.writer.Close()
	if _, ok := p.writer.Balancer.(*kafka.Hash); !ok {
		t.Fatalf("lifecycle writer balancer = %T, want *kafka.Hash (LeastBytes ignores the key)", p.writer.Balancer)
	}
}

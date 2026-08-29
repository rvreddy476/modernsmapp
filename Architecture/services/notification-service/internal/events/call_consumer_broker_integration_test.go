//go:build integration

package events

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// Live-broker proofs for CALL-LB-4 against a REAL Redpanda/Kafka: the full
// production consumer — kafka.Reader FetchMessage/CommitMessages, the real
// kafkaDLQ writer, the real Start loop and processMessage classification —
// with only the notifier seam scripted (its Scylla LWT idempotency guarantee
// is pinned separately by the service). DISPOSABLE unique topics and
// consumer groups; topics are deleted afterwards.
//
// Run:
//
//	CALL_KAFKA_BROKER=localhost:9092 \
//	  go test -tags integration -run CallConsumerBroker ./internal/events/ -count=1 -v
func brokerAddr(t *testing.T) string {
	t.Helper()
	addr := os.Getenv("CALL_KAFKA_BROKER")
	if addr == "" {
		t.Skip("CALL_KAFKA_BROKER not set")
	}
	return addr
}

func disposableTopic(t *testing.T, addr string) string {
	t.Helper()
	topic := fmt.Sprintf("call.notifications.it.%s", uuid.New().String()[:8])
	conn, err := kafka.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	controller, err := conn.Controller()
	if err != nil {
		t.Fatal(err)
	}
	cc, err := kafka.Dial("tcp", net.JoinHostPort(controller.Host, strconv.Itoa(controller.Port)))
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()
	if err := cc.CreateTopics(kafka.TopicConfig{
		Topic: topic, NumPartitions: 1, ReplicationFactor: 1,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if conn, err := kafka.Dial("tcp", addr); err == nil {
			defer conn.Close()
			if controller, err := conn.Controller(); err == nil {
				if cc, err := kafka.Dial("tcp",
					net.JoinHostPort(controller.Host, strconv.Itoa(controller.Port))); err == nil {
					defer cc.Close()
					_ = cc.DeleteTopics(topic, topic+".dlq")
				}
			}
		}
	})
	return topic
}

func produce(t *testing.T, addr, topic string, value []byte) {
	t.Helper()
	w := &kafka.Writer{
		Addr:                   kafka.TCP(addr),
		Topic:                  topic,
		RequiredAcks:           kafka.RequireAll,
		AllowAutoTopicCreation: true,
	}
	defer w.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := w.WriteMessages(ctx, kafka.Message{Value: value}); err != nil {
		t.Fatal(err)
	}
}

func liveConsumer(t *testing.T, addr, topic, group string, notifier callNotifier) (*CallConsumer, func(), chan struct{}) {
	t.Helper()
	c := NewCallConsumer([]string{addr}, group, topic, notifier)
	c.retryBackoff = 20 * time.Millisecond // test pace; semantics unchanged
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Start(ctx)
	}()
	stop := func() {
		cancel()
		<-done
		_ = c.Close()
	}
	return c, stop, done
}

// Criterion: valid invite processing fails at least six times on a
// dependency, the offset remains uncommitted throughout, the dependency
// recovers, the SAME event succeeds, and exactly one idempotent create with
// the event-bound identity results. Commit is then proven behaviorally: a
// fresh consumer in the SAME group sees only a later event, never the first.
func TestCallConsumerBrokerTransientOutageThenRecovery(t *testing.T) {
	addr := brokerAddr(t)
	topic := disposableTopic(t, addr)
	group := "g-" + uuid.New().String()[:8]
	inviteeA := uuid.New()

	produce(t, addr, topic, validInviteMessage(t, 0, "live-evt-A", inviteeA).Value)

	notifier := &flakyNotifier{failFirst: 6}
	_, stop, _ := liveConsumer(t, addr, topic, group, notifier)

	waitForLong(t, "6 transient failures then success", func() bool {
		attempts, _, rows := notifier.state()
		return attempts >= 7 && rows == 1
	})
	_, identities, _ := notifier.state()
	if want := "call:live-evt-A:" + inviteeA.String(); identities[0] != want {
		t.Fatalf("identity %q, want %q", identities[0], want)
	}
	// Give the commit time to land before stopping.
	time.Sleep(500 * time.Millisecond)
	stop()

	// SAME group again: the first event's offset is committed, so only the
	// newly produced second event may arrive.
	inviteeB := uuid.New()
	produce(t, addr, topic, validInviteMessage(t, 0, "live-evt-B", inviteeB).Value)
	notifier2 := &flakyNotifier{}
	_, stop2, _ := liveConsumer(t, addr, topic, group, notifier2)
	defer stop2()
	waitForLong(t, "second event processed by the restarted group", func() bool {
		_, ids, _ := notifier2.state()
		return len(ids) >= 1
	})
	_, ids, _ := notifier2.state()
	for _, id := range ids {
		if strings.Contains(id, "live-evt-A") {
			t.Fatalf("committed event was redelivered after restart: %v", ids)
		}
	}
	if !strings.Contains(ids[0], "live-evt-B") {
		t.Fatalf("second event not delivered: %v", ids)
	}
}

// Criterion: stop the consumer DURING a transient failure (nothing
// committed), restart with the same group, and observe the same event again
// — the broker's redelivery is the recovery path, and the event-bound
// identity keeps the durable row single.
func TestCallConsumerBrokerRestartRedeliversUncommitted(t *testing.T) {
	addr := brokerAddr(t)
	topic := disposableTopic(t, addr)
	group := "g-" + uuid.New().String()[:8]
	invitee := uuid.New()

	produce(t, addr, topic, validInviteMessage(t, 0, "live-evt-R", invitee).Value)

	failing := &flakyNotifier{failFirst: 1 << 30}
	_, stop, _ := liveConsumer(t, addr, topic, group, failing)
	waitForLong(t, "retries in flight", func() bool {
		attempts, _, _ := failing.state()
		return attempts >= 3
	})
	stop() // shutdown mid-retry: Start committed nothing

	healthy := &flakyNotifier{}
	_, stop2, _ := liveConsumer(t, addr, topic, group, healthy)
	defer stop2()
	waitForLong(t, "the SAME event redelivered to the restarted group", func() bool {
		_, ids, _ := healthy.state()
		return len(ids) == 1
	})
	_, ids, rows := healthy.state()
	if want := "call:live-evt-R:" + invitee.String(); ids[0] != want {
		t.Fatalf("redelivered identity %q, want %q", ids[0], want)
	}
	if rows != 1 {
		t.Fatalf("rows = %d", rows)
	}
}

// Criterion: a permanently malformed event is written to the DURABLE DLQ
// topic (with its reason) before the source offset commits; the partition
// then moves on to healthy events. (The quarantine-failure arm — a failed
// DLQ write blocking the commit — is pinned by the scripted-seam unit test
// TestCallConsumerQuarantineFailureBlocksCommit.)
func TestCallConsumerBrokerPoisonLandsInDLQBeforeCommit(t *testing.T) {
	addr := brokerAddr(t)
	topic := disposableTopic(t, addr)
	group := "g-" + uuid.New().String()[:8]

	produce(t, addr, topic, []byte(`{this is not json`))
	invitee := uuid.New()
	produce(t, addr, topic, validInviteMessage(t, 0, "live-evt-P", invitee).Value)

	notifier := &flakyNotifier{}
	_, stop, _ := liveConsumer(t, addr, topic, group, notifier)
	defer stop()

	// The healthy event behind the poison must flow — which also proves the
	// poison offset committed, which the loop only allows AFTER quarantine.
	waitForLong(t, "healthy event processed past the poison", func() bool {
		_, ids, _ := notifier.state()
		return len(ids) == 1
	})

	// The DLQ topic holds the poison bytes with the reason header.
	dlqReader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: []string{addr},
		Topic:   topic + ".dlq",
		GroupID: "dlq-check-" + uuid.New().String()[:8],
	})
	defer dlqReader.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	m, err := dlqReader.FetchMessage(ctx)
	if err != nil {
		t.Fatalf("no DLQ record for the poison message: %v", err)
	}
	if string(m.Value) != `{this is not json` {
		t.Fatalf("DLQ record does not carry the original bytes: %q", m.Value)
	}
	var reason string
	for _, h := range m.Headers {
		if h.Key == "x-quarantine-reason" {
			reason = string(h.Value)
		}
	}
	if !strings.Contains(reason, "malformed envelope JSON") {
		t.Fatalf("DLQ record missing its reason header: %q", reason)
	}
}

func waitForLong(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

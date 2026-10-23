//go:build integration

package events

import (
	"context"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/google/uuid"
)

// Live-broker proof for the membership consumer against a REAL Redpanda/Kafka:
// the production kafka.Reader, Start loop and commit path, with only the
// notifier seam scripted. A poison message ahead of a valid membership.started
// is committed and skipped, the valid event is delivered to both parties, and
// a fresh consumer in the SAME group sees only what was produced after.
//
// Run:
//
//	CALL_KAFKA_BROKER=localhost:9092 \
//	  go test -tags integration -run MembershipConsumerBroker ./internal/events/ -count=1 -v
func TestMembershipConsumerBrokerPoisonThenValidCommits(t *testing.T) {
	addr := brokerAddr(t)
	topic := disposableTopic(t, addr)
	group := "g-ms-" + uuid.New().String()[:8]

	produce(t, addr, topic, []byte("not json"))
	produce(t, addr, topic, msMessage(t, msEnvelope(t, EventMembershipStarted, "it-start", msPayload(nil)), 0).Value)

	notify := &fakeMembershipNotifier{}
	stop := liveMembershipConsumer(t, addr, topic, group, notify)
	waitForLong(t, "started delivered to creator and member", func() bool {
		notify.mu.Lock()
		defer notify.mu.Unlock()
		return len(notify.got) == 2
	})
	time.Sleep(500 * time.Millisecond) // let the commit land
	stop()
	if notify.got[0].Type != service.MembershipTypeNewMember || notify.got[1].Type != service.MembershipTypeJoined {
		t.Fatalf("delivered %+v", notify.got)
	}

	produce(t, addr, topic, msMessage(t, msEnvelope(t, EventMembershipEnded, "it-end", msPayload(nil)), 0).Value)
	again := &fakeMembershipNotifier{}
	stop = liveMembershipConsumer(t, addr, topic, group, again)
	waitForLong(t, "only the later event", func() bool {
		again.mu.Lock()
		defer again.mu.Unlock()
		return len(again.got) >= 1
	})
	time.Sleep(500 * time.Millisecond)
	stop()
	if len(again.got) != 1 || again.got[0].Type != service.MembershipTypeEnded || again.got[0].EventID != "it-end" {
		t.Fatalf("restart redelivered committed events: %+v", again.got)
	}
}

func liveMembershipConsumer(t *testing.T, addr, topic, group string, notify membershipNotifier) func() {
	t.Helper()
	c := NewMembershipConsumerWithDialer([]string{addr}, group, topic, nil, nil)
	c.notify = notify
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Start(ctx)
	}()
	return func() {
		cancel()
		<-done
		_ = c.Close()
	}
}

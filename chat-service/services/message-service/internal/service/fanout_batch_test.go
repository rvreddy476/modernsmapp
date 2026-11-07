package service

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// batchRecorder is a go-redis hook that answers every command itself and
// records how it was sent: one call per command, or one pipelined batch.
type batchRecorder struct {
	mu        sync.Mutex
	singles   [][]interface{}
	pipelines [][][]interface{}
}

func (r *batchRecorder) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) { return next(ctx, network, addr) }
}

func (r *batchRecorder) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, cmd redis.Cmder) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.singles = append(r.singles, cmd.Args())
		return nil
	}
}

func (r *batchRecorder) ProcessPipelineHook(redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(_ context.Context, cmds []redis.Cmder) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		batch := make([][]interface{}, len(cmds))
		for i, c := range cmds {
			batch[i] = c.Args()
		}
		r.pipelines = append(r.pipelines, batch)
		return nil
	}
}

// publishedTo lists the channels PUBLISHed to, in pipelines and singly.
func (r *batchRecorder) publishedTo() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	add := func(args []interface{}) {
		if len(args) >= 2 && fmt.Sprint(args[0]) == "publish" {
			out = append(out, fmt.Sprint(args[1]))
		}
	}
	for _, c := range r.singles {
		add(c)
	}
	for _, p := range r.pipelines {
		for _, c := range p {
			add(c)
		}
	}
	return out
}

func batchRecordingRedis(t *testing.T) (*redis.Client, *batchRecorder) {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	rec := &batchRecorder{}
	rdb.AddHook(rec)
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb, rec
}

// --- typing: the request gate (review 7 Nov 2026) ---

func typingRequestFixture(t *testing.T, status string, expires time.Time) (*Service, *batchRecorder, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	f := newGovernanceFake()
	requester, recipient, convID := uuid.New(), uuid.New(), uuid.New()
	f.conversations[convID] = &postgres.Conversation{ID: convID, Type: "direct", IsRequest: true}
	f.roles[convID] = map[uuid.UUID]string{requester: "member", recipient: "member"}
	f.freshPolicy(requester, false)
	f.freshPolicy(recipient, false)
	f.latestRequest = &postgres.MessageRequest{ConversationID: convID, SenderID: requester, ReceiverID: recipient,
		Status: status, ExpiresAt: expires}
	rdb, rec := batchRecordingRedis(t)
	return &Service{convStore: f, rdb: rdb, log: slog.Default()}, rec, convID, requester, recipient
}

func TestTypingIsRefusedToTheRequesterOfAPendingRequest(t *testing.T) {
	ctx := context.Background()
	svc, rec, convID, requester, recipient := typingRequestFixture(t, "pending", time.Now().Add(time.Hour))

	if err := svc.SetTyping(ctx, requester, convID); err != nil {
		t.Fatalf("requester typing: %v (a refusal is silent)", err)
	}
	if got := len(rec.singles) + len(rec.pipelines); got != 0 {
		t.Fatalf("the requester of a pending request reached Redis (%d calls): the recipient would see a stranger typing", got)
	}

	// The recipient may write (replying accepts), so they may type.
	if err := svc.SetTyping(ctx, recipient, convID); err != nil {
		t.Fatal(err)
	}
	if got := rec.publishedTo(); len(got) != 1 || got[0] != "chat:"+requester.String() {
		t.Fatalf("recipient typing published to %v, want only the requester's channel", got)
	}
}

func TestTypingIsRefusedInAClosedRequest(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		status  string
		expires time.Time
	}{
		"declined": {"ignored", time.Now().Add(time.Hour)},
		"expired":  {"pending", time.Now().Add(-time.Minute)},
	} {
		svc, rec, convID, _, recipient := typingRequestFixture(t, tc.status, tc.expires)
		if err := svc.SetTyping(ctx, recipient, convID); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := rec.publishedTo(); len(got) != 0 {
			t.Fatalf("%s request: typing published to %v, want nothing (nobody may write)", name, got)
		}
	}
}

// --- typing: one pipelined round trip (audit M4) ---

func TestTypingFanOutIsOnePipelinedCall(t *testing.T) {
	ctx := context.Background()
	f := newGovernanceFake()
	typer, convID := uuid.New(), uuid.New()
	members := map[uuid.UUID]string{}
	for i := 0; i < 40; i++ {
		members[uuid.New()] = "member"
	}
	f.addGroup(convID, typer, members)
	f.freshPolicy(typer, false)
	rdb, rec := batchRecordingRedis(t)
	svc := &Service{convStore: f, rdb: rdb, log: slog.Default()}

	if err := svc.SetTyping(ctx, typer, convID); err != nil {
		t.Fatal(err)
	}
	if len(rec.singles) != 0 || len(rec.pipelines) != 1 {
		t.Fatalf("typing made %d single calls and %d pipelines, want one pipeline", len(rec.singles), len(rec.pipelines))
	}
	got := rec.publishedTo()
	if len(got) != len(members) {
		t.Fatalf("typing reached %d channels, want every other member (%d)", len(got), len(members))
	}
	for _, ch := range got {
		if ch == "chat:"+typer.String() {
			t.Fatal("the typer was sent their own typing frame")
		}
	}
}

// --- receipts: one pipelined round trip per fan-out (audit M4) ---

func groupTicksFixture(t *testing.T, size int) (*Service, *ticksFake, *batchRecorder, uuid.UUID, uuid.UUID, []uuid.UUID) {
	t.Helper()
	f := newTicksFake()
	actor, conv := uuid.New(), uuid.New()
	f.conversations[conv] = &postgres.Conversation{ID: conv, Type: "group"}
	f.roles[conv] = map[uuid.UUID]string{actor: "owner"}
	f.joined[actor] = at(0)
	f.freshPolicy(actor, false)
	var others []uuid.UUID
	for i := 0; i < size; i++ {
		u := uuid.New()
		others = append(others, u)
		f.roles[conv][u] = "member"
		f.joined[u] = at(0)
		f.freshPolicy(u, false)
		f.delivered[u] = postgres.DeliveredCursor{ConversationID: conv, UserID: u, LastDeliveredAt: at(30)}
		f.cursors[u] = postgres.ReadCursor{UserID: u, LastReadAt: at(30)}
	}
	s := ticksService(f)
	rdb, rec := batchRecordingRedis(t)
	s.rdb = rdb
	return s, f, rec, conv, actor, others
}

func assertOneBatchTo(t *testing.T, rec *batchRecorder, what string, want []uuid.UUID) {
	t.Helper()
	if len(rec.singles) != 0 || len(rec.pipelines) != 1 {
		t.Fatalf("%s: %d single calls and %d pipelines, want one pipeline", what, len(rec.singles), len(rec.pipelines))
	}
	got := map[string]bool{}
	for _, ch := range rec.publishedTo() {
		got[ch] = true
	}
	if len(got) != len(want) {
		t.Fatalf("%s reached %d members, want %d", what, len(got), len(want))
	}
	for _, u := range want {
		if !got["chat:"+u.String()] {
			t.Fatalf("%s skipped member %s", what, u)
		}
	}
}

func TestGroupDeliveryReceiptFanOutIsOnePipelinedCall(t *testing.T) {
	s, _, rec, conv, actor, others := groupTicksFixture(t, 30)
	ts := at(50)
	if _, err := s.MarkDelivered(context.Background(), actor, conv, nil, &ts); err != nil {
		t.Fatal(err)
	}
	// Everyone else had acked at(30); the actor's ack moves each of their
	// "delivered to everyone" watermarks from the actor's join to at(30).
	assertOneBatchTo(t, rec, "delivery receipts", others)
}

func TestGroupReadReceiptFanOutIsOnePipelinedCall(t *testing.T) {
	s, f, rec, conv, actor, others := groupTicksFixture(t, 30)
	f.cursors[actor] = postgres.ReadCursor{UserID: actor, LastReadAt: at(50)}
	s.publishGroupReadAggregate(context.Background(), conv, actor, time.Time{})
	assertOneBatchTo(t, rec, "read receipts", others)
}

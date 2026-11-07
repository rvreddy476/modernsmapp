package events

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// The chat push consumer (7 Nov 2026): a broker read error used to end the
// loop for good, and a failed delivery was dropped on the first error. These
// drive the production Start loop over a scripted broker.

type chatFetch struct {
	msg kafka.Message
	err error
}

// scriptedChatSource replays fetches, then cancels the run and reports the
// cancellation, the way kafka.Reader does on shutdown.
type scriptedChatSource struct {
	fetches []chatFetch
	cancel  context.CancelFunc
	commits []kafka.Message
}

func (s *scriptedChatSource) FetchMessage(ctx context.Context) (kafka.Message, error) {
	if len(s.fetches) == 0 {
		s.cancel()
		return kafka.Message{}, ctx.Err()
	}
	next := s.fetches[0]
	s.fetches = s.fetches[1:]
	return next.msg, next.err
}

func (s *scriptedChatSource) CommitMessages(_ context.Context, msgs ...kafka.Message) error {
	s.commits = append(s.commits, msgs...)
	return nil
}

// flakyChatNotifier fails each user's first failures[user] deliveries and counts
// every attempt. onFail, when set, runs on each failure.
type flakyChatNotifier struct {
	failures  map[uuid.UUID]int
	attempts  map[uuid.UUID]int
	delivered map[uuid.UUID]int
	deepLinks map[uuid.UUID]string
	onFail    func()
}

func newFlakyChatNotifier(failures map[uuid.UUID]int) *flakyChatNotifier {
	return &flakyChatNotifier{
		failures:  failures,
		attempts:  map[uuid.UUID]int{},
		delivered: map[uuid.UUID]int{},
		deepLinks: map[uuid.UUID]string{},
	}
}

func (f *flakyChatNotifier) record(userID uuid.UUID, deepLink string) error {
	f.attempts[userID]++
	if f.attempts[userID] <= f.failures[userID] {
		if f.onFail != nil {
			f.onFail()
		}
		return errors.New("scylla unavailable")
	}
	f.delivered[userID]++
	f.deepLinks[userID] = deepLink
	return nil
}

func (f *flakyChatNotifier) CreateNotification(_ context.Context, userID, _ uuid.UUID, _, _ string, _ uuid.UUID, deepLink string, _ time.Time) error {
	return f.record(userID, deepLink)
}

func (f *flakyChatNotifier) CreateChatMessageNotification(_ context.Context, userID, _, _ uuid.UUID, deepLink string, _ time.Time, _ service.ChatMessagePush, _ bool) error {
	return f.record(userID, deepLink)
}

func (f *flakyChatNotifier) CreateHiddenChatMessagePush(_ context.Context, userID uuid.UUID, _ bool) error {
	return f.record(userID, "")
}

// runChatConsumer runs Start to completion over the script, failing the test
// if the loop does not end once the script is exhausted.
func runChatConsumer(t *testing.T, notifier chatNotifier, fetches ...chatFetch) *scriptedChatSource {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src := &scriptedChatSource{fetches: fetches, cancel: cancel}
	consumer := &ChatConsumer{source: src, service: notifier}
	done := make(chan struct{})
	go func() {
		consumer.Start(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after the context was cancelled")
	}
	return src
}

func chatMessageEvent(t *testing.T, conversationType string, conversationID, senderID uuid.UUID, recipients ...uuid.UUID) kafka.Message {
	t.Helper()
	ids := make([]string, len(recipients))
	for i, r := range recipients {
		ids[i] = r.String()
	}
	payload, err := json.Marshal(messageCreatedPayload{
		MessageID:        uuid.NewString(),
		ConversationID:   conversationID.String(),
		ConversationType: conversationType,
		SenderID:         senderID.String(),
		Type:             "text",
		CreatedAt:        time.Now().UTC(),
		RecipientIDs:     ids,
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := json.Marshal(chatEnvelope{EventID: uuid.NewString(), EventType: chatEventMessageCreated, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	return kafka.Message{Value: envelope, Offset: 7}
}

func TestChatConsumerSurvivesFetchErrors(t *testing.T) {
	sender, recipient := uuid.New(), uuid.New()
	notifier := newFlakyChatNotifier(nil)
	msg := chatMessageEvent(t, "", uuid.New(), sender, recipient)

	src := runChatConsumer(t, notifier,
		chatFetch{err: errors.New("broker connection reset")},
		chatFetch{err: errors.New("group coordinator not available")},
		chatFetch{msg: msg},
	)

	if notifier.delivered[recipient] != 1 {
		t.Fatalf("message after two fetch errors delivered %d times; want 1 (the loop must keep reading)", notifier.delivered[recipient])
	}
	if len(src.commits) != 1 {
		t.Fatalf("commits = %d; want 1", len(src.commits))
	}
}

// A closed reader (io.EOF) can never fetch again: stop, do not spin.
func TestChatConsumerStopsWhenTheReaderIsClosed(t *testing.T) {
	sender, recipient := uuid.New(), uuid.New()
	notifier := newFlakyChatNotifier(nil)

	src := runChatConsumer(t, notifier,
		chatFetch{err: io.EOF},
		chatFetch{msg: chatMessageEvent(t, "", uuid.New(), sender, recipient)},
	)

	if notifier.attempts[recipient] != 0 || len(src.commits) != 0 {
		t.Fatalf("consumer kept reading after the reader closed: attempts=%d commits=%d", notifier.attempts[recipient], len(src.commits))
	}
}

func TestChatConsumerRetriesAFailedPushThenCommits(t *testing.T) {
	sender, recipient := uuid.New(), uuid.New()
	notifier := newFlakyChatNotifier(map[uuid.UUID]int{recipient: 2})

	src := runChatConsumer(t, notifier, chatFetch{msg: chatMessageEvent(t, "", uuid.New(), sender, recipient)})

	if notifier.attempts[recipient] != 3 || notifier.delivered[recipient] != 1 {
		t.Fatalf("attempts=%d delivered=%d; want 3 attempts and 1 delivery", notifier.attempts[recipient], notifier.delivered[recipient])
	}
	if len(src.commits) != 1 {
		t.Fatalf("commits = %d; want 1 after the retried delivery", len(src.commits))
	}
}

func TestChatConsumerGivesUpAfterBoundedAttempts(t *testing.T) {
	sender, recipient := uuid.New(), uuid.New()
	notifier := newFlakyChatNotifier(map[uuid.UUID]int{recipient: 1000})

	src := runChatConsumer(t, notifier, chatFetch{msg: chatMessageEvent(t, "", uuid.New(), sender, recipient)})

	if notifier.attempts[recipient] != chatDeliveryAttempts {
		t.Fatalf("attempts = %d; want exactly %d", notifier.attempts[recipient], chatDeliveryAttempts)
	}
	// Given up on (and logged), so the partition moves on.
	if len(src.commits) != 1 {
		t.Fatalf("commits = %d; want 1 once the message is given up on", len(src.commits))
	}
}

// Retrying one recipient must not re-deliver to the others.
func TestChatConsumerRetryDoesNotDuplicateOtherRecipients(t *testing.T) {
	sender, broken, healthy := uuid.New(), uuid.New(), uuid.New()
	notifier := newFlakyChatNotifier(map[uuid.UUID]int{broken: 2})

	runChatConsumer(t, notifier, chatFetch{msg: chatMessageEvent(t, "", uuid.New(), sender, broken, healthy)})

	if notifier.delivered[healthy] != 1 || notifier.delivered[broken] != 1 {
		t.Fatalf("delivered healthy=%d broken=%d; want 1 each", notifier.delivered[healthy], notifier.delivered[broken])
	}
}

func TestChatConsumerShutdownMidRetryCommitsNothing(t *testing.T) {
	sender, recipient := uuid.New(), uuid.New()
	notifier := newFlakyChatNotifier(map[uuid.UUID]int{recipient: 1000})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	notifier.onFail = cancel // shutdown arrives during the first failure
	src := &scriptedChatSource{
		fetches: []chatFetch{{msg: chatMessageEvent(t, "", uuid.New(), sender, recipient)}},
		cancel:  cancel,
	}
	consumer := &ChatConsumer{source: src, service: notifier, retryBackoff: time.Hour}

	done := make(chan struct{})
	go func() { consumer.Start(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return on shutdown during a retry")
	}
	if len(src.commits) != 0 {
		t.Fatalf("commits = %d; an undelivered message must stay uncommitted on shutdown", len(src.commits))
	}
}

// A malformed event cannot succeed on retry: no delivery, committed once.
func TestChatConsumerCommitsMalformedEventWithoutRetry(t *testing.T) {
	notifier := newFlakyChatNotifier(nil)
	src := runChatConsumer(t, notifier, chatFetch{msg: kafka.Message{Value: []byte("{not json")}})

	if len(notifier.attempts) != 0 {
		t.Fatalf("malformed event reached the notifier: %v", notifier.attempts)
	}
	if len(src.commits) != 1 {
		t.Fatalf("commits = %d; want 1", len(src.commits))
	}
}

func TestChatPushDeepLinkByConversationKind(t *testing.T) {
	sender, a, b := uuid.New(), uuid.New(), uuid.New()
	conversationID := uuid.New()
	groupLink := "/messenger?lane=groups&group=" + conversationID.String()
	directLink := "/messenger?user=" + sender.String()

	cases := []struct {
		name             string
		conversationType string
		recipients       []uuid.UUID
		want             string
	}{
		{"group by recipient count", "", []uuid.UUID{a, b}, groupLink},
		{"direct by recipient count", "", []uuid.UUID{a}, directLink},
		{"two-person group named by chat", "group", []uuid.UUID{a}, groupLink},
		{"direct named by chat", "direct", []uuid.UUID{a, b}, directLink},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			notifier := newFlakyChatNotifier(nil)
			consumer := &ChatConsumer{service: notifier}
			msg := chatMessageEvent(t, tc.conversationType, conversationID, sender, tc.recipients...)
			if err := consumer.processMessage(context.Background(), msg); err != nil {
				t.Fatal(err)
			}
			for _, r := range tc.recipients {
				if got := notifier.deepLinks[r]; got != tc.want {
					t.Errorf("deep link for %s = %q; want %q", r, got, tc.want)
				}
			}
		})
	}
}

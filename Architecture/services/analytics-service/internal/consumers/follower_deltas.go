package consumers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	pgstore "github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// FollowerDeltaConsumer folds graph-service's follow-edge events into
// analytics.creator_follower_daily (migration 013), the source of
// followers_delta on GET /v1/analytics/creator/me.
//
// Which events count, and why (graph-service is the producer):
//
//   - UserFollowed is written to graph's outbox only when a NEW follow
//     edge lands — a direct follow of a public account, or the ACCEPT of a
//     follow request to a private one. graph.follow_requested is only a
//     request and is ignored. One follow for the followee.
//   - UserUnfollowed is published on every unfollow call, including a
//     repeat on a pair that no longer follows. edge_removed says whether
//     an edge actually went; false is skipped, absent (a producer that
//     predates the field) is counted. One unfollow for the followee.
//   - UserBlocked severs follows in both directions with no
//     UserUnfollowed; removed_follow_forward/_reverse say which edges it
//     removed. Each is an unfollow for the account that was being
//     followed.
//
// Idempotency is the receipt table: the event id and the counter change
// commit together, so a redelivery (Kafka at-least-once, or the outbox
// relay re-publishing a row it could not mark — it reuses the row id as
// the event id) is recognised and changes nothing. Counting is
// commutative, so the different partition keys graph uses for follows
// (canonical pair) and unfollows (follower) cannot reorder a result.
type FollowerDeltaConsumer struct {
	store followerDeltaStore
	now   func() time.Time
}

type followerDeltaStore interface {
	ApplyFollowerEvent(ctx context.Context, eventID, eventType string, deltas []pgstore.FollowerDelta) (bool, error)
	PruneFollowerReceipts(ctx context.Context, before time.Time) (int64, error)
}

// FollowerDeltaGroupID is the consumer group. It is new, so on first
// start it begins at the END of the topic (see Start): history starts at
// deploy, matching follower_tracking.started_at.
const FollowerDeltaGroupID = "analytics-follower-deltas"

// followerReceiptTTL is how long a receipt is kept. Far past the topic's
// retention, so nothing older can be redelivered.
const followerReceiptTTL = 30 * 24 * time.Hour

func NewFollowerDeltaConsumer(store followerDeltaStore) *FollowerDeltaConsumer {
	return &FollowerDeltaConsumer{store: store, now: time.Now}
}

type permanentFollowerError struct{ err error }

func (e permanentFollowerError) Error() string { return e.err.Error() }
func (e permanentFollowerError) Unwrap() error { return e.err }

// Start consumes until ctx is cancelled. Same durability rule as the
// ownership projection: a database failure keeps the record in flight
// and retries it; only an undecodable or invalid record is committed
// past.
func (c *FollowerDeltaConsumer) Start(ctx context.Context, brokers []string, topic string, dialer *kafka.Dialer) {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: brokers, GroupID: FollowerDeltaGroupID, Topic: topic,
		MinBytes: 1, MaxBytes: 10e6, Dialer: dialer,
		// Only consulted when the group has no committed offset, i.e. the
		// first start. Replaying the retained topic would count days
		// before follower_tracking.started_at; tracking starts now.
		StartOffset: kafka.LastOffset,
	})
	defer reader.Close()

	go c.pruneReceipts(ctx)

	for {
		message, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("follower delta fetch failed", "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		for {
			err = c.apply(ctx, message.Value)
			var permanent permanentFollowerError
			if errors.As(err, &permanent) {
				slog.Warn("follower delta record skipped", "error", err, "offset", message.Offset)
				err = nil
			}
			if err == nil {
				if commitErr := reader.CommitMessages(ctx, message); commitErr != nil {
					if ctx.Err() != nil {
						return
					}
					slog.Error("follower delta commit failed", "error", commitErr)
				}
				break
			}
			slog.Error("follower delta durable apply failed; retrying same record", "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(250 * time.Millisecond):
			}
		}
	}
}

func (c *FollowerDeltaConsumer) pruneReceipts(ctx context.Context) {
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	for {
		if n, err := c.store.PruneFollowerReceipts(ctx, c.now().Add(-followerReceiptTTL)); err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("follower receipt prune failed", "error", err)
		} else if n > 0 {
			slog.Info("follower receipts pruned", "deleted", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (c *FollowerDeltaConsumer) apply(ctx context.Context, value []byte) error {
	var envelope events.EventEnvelope
	if err := json.Unmarshal(value, &envelope); err != nil {
		return permanentFollowerError{err}
	}
	deltas, relevant, err := followerDeltasFor(envelope, c.now)
	if err != nil {
		return err
	}
	if !relevant {
		return nil
	}
	if envelope.EventID == "" || len(envelope.EventID) > 128 {
		return permanentFollowerError{fmt.Errorf("%s has no usable event_id", envelope.EventType)}
	}
	_, err = c.store.ApplyFollowerEvent(ctx, envelope.EventID, envelope.EventType, deltas)
	return err
}

// followerDeltasFor maps one envelope to its follower changes. relevant
// is false for every event type this consumer does not count; an event
// that is relevant but changes nothing (a repeat unfollow, a block that
// severed no follow) returns relevant with no deltas, and still gets a
// receipt.
func followerDeltasFor(envelope events.EventEnvelope, now func() time.Time) (deltas []pgstore.FollowerDelta, relevant bool, err error) {
	switch envelope.EventType {
	case events.UserFollowed:
		var p events.UserFollowedPayload
		if err := json.Unmarshal(envelope.Payload, &p); err != nil {
			return nil, false, permanentFollowerError{err}
		}
		follower, followee, err := followPair(envelope.EventType, p.FollowerID, p.FolloweeID)
		if err != nil {
			return nil, false, err
		}
		if follower == followee {
			return nil, true, nil
		}
		day := eventDay(p.CreatedAt, envelope.OccurredAt, now)
		return []pgstore.FollowerDelta{{CreatorID: followee, Day: day, Follows: 1}}, true, nil

	case events.UserUnfollowed:
		var p events.UserUnfollowedPayload
		if err := json.Unmarshal(envelope.Payload, &p); err != nil {
			return nil, false, permanentFollowerError{err}
		}
		follower, followee, err := followPair(envelope.EventType, p.FollowerID, p.FolloweeID)
		if err != nil {
			return nil, false, err
		}
		if follower == followee || (p.EdgeRemoved != nil && !*p.EdgeRemoved) {
			return nil, true, nil
		}
		day := eventDay(p.OccurredAt, envelope.OccurredAt, now)
		return []pgstore.FollowerDelta{{CreatorID: followee, Day: day, Unfollows: 1}}, true, nil

	case events.UserBlocked:
		var p events.UserBlockedPayload
		if err := json.Unmarshal(envelope.Payload, &p); err != nil {
			return nil, false, permanentFollowerError{err}
		}
		if !p.RemovedFollowForward && !p.RemovedFollowReverse {
			// Nothing severed, or a producer that does not report it.
			return nil, false, nil
		}
		blocker, blocked, err := followPair(envelope.EventType, p.BlockerID, p.BlockedID)
		if err != nil {
			return nil, false, err
		}
		if blocker == blocked {
			return nil, true, nil
		}
		day := eventDay(p.BlockedAt, envelope.OccurredAt, now)
		if p.RemovedFollowForward { // blocker was following blocked
			deltas = append(deltas, pgstore.FollowerDelta{CreatorID: blocked, Day: day, Unfollows: 1})
		}
		if p.RemovedFollowReverse { // blocked was following blocker
			deltas = append(deltas, pgstore.FollowerDelta{CreatorID: blocker, Day: day, Unfollows: 1})
		}
		return deltas, true, nil
	}
	return nil, false, nil
}

func followPair(eventType, a, b string) (uuid.UUID, uuid.UUID, error) {
	first, err := uuid.Parse(a)
	if err != nil || first == uuid.Nil {
		return uuid.Nil, uuid.Nil, permanentFollowerError{fmt.Errorf("%s has an invalid first user id", eventType)}
	}
	second, err := uuid.Parse(b)
	if err != nil || second == uuid.Nil {
		return uuid.Nil, uuid.Nil, permanentFollowerError{fmt.Errorf("%s has an invalid second user id", eventType)}
	}
	return first, second, nil
}

// eventDay is the UTC day the change happened: the producer's timestamp,
// else the envelope's, else now.
func eventDay(at, occurredAt time.Time, now func() time.Time) time.Time {
	switch {
	case !at.IsZero():
		return at.UTC()
	case !occurredAt.IsZero():
		return occurredAt.UTC()
	}
	return now().UTC()
}

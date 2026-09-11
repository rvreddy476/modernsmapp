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
	"github.com/atpost/shared/postclassify"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

type ownershipStore interface {
	UpsertContentOwnership(context.Context, pgstore.ContentOwnership) error
	UpdateContentType(ctx context.Context, contentID, creatorID uuid.UUID, contentType string) (bool, error)
	RecordContentTypeExclusion(context.Context, pgstore.ContentTypeExclusion) error
	ClearContentTypeExclusion(ctx context.Context, contentID uuid.UUID) error
	ApplyContentEligibility(context.Context, pgstore.ContentEligibility) (bool, error)
}

// unknownContentType is what the ownership row stores when the
// producer's content_type is not a monetizable kind. It is not a label
// anything downstream recognises — monetization's rate lookup skips it,
// model.IsDisplayView treats it as unlabelled — which is the point. The
// old default was "post", which looked like a kind and was not one.
const unknownContentType = "unknown"

type permanentOwnershipError struct{ err error }

func (e permanentOwnershipError) Error() string { return e.err.Error() }
func (e permanentOwnershipError) Unwrap() error { return e.err }

type ContentOwnershipConsumer struct {
	store ownershipStore
}

func NewContentOwnershipConsumer(store ownershipStore) *ContentOwnershipConsumer {
	return &ContentOwnershipConsumer{store: store}
}

// Start projects immutable content ownership, its canonical kind, and
// its monetization eligibility. A durable failure keeps the fetched
// record in flight and stalls the partition; only syntactically
// undecodable or permanently invalid records advance.
func (c *ContentOwnershipConsumer) Start(ctx context.Context, brokers []string, topic string, dialer *kafka.Dialer) {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: brokers, GroupID: "analytics-content-ownership", Topic: topic,
		MinBytes: 1, MaxBytes: 10e6, Dialer: dialer,
	})
	defer reader.Close()

	for {
		message, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("ownership projection fetch failed", "error", err)
			continue
		}
		for {
			err = c.apply(ctx, message.Value)
			var permanent permanentOwnershipError
			if err == nil || errors.As(err, &permanent) {
				if commitErr := reader.CommitMessages(ctx, message); commitErr != nil {
					slog.Error("ownership projection commit failed", "error", commitErr)
					continue
				}
				break
			}
			slog.Error("ownership projection durable apply failed; retrying same record", "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(250 * time.Millisecond):
			}
		}
	}
}

func (c *ContentOwnershipConsumer) apply(ctx context.Context, value []byte) error {
	var envelope events.EventEnvelope
	if err := json.Unmarshal(value, &envelope); err != nil {
		return permanentOwnershipError{err}
	}
	switch envelope.EventType {
	case events.PostCreated:
		return c.applyCreated(ctx, envelope)
	case events.PostContentTypeChanged:
		return c.applyReclassification(ctx, envelope)
	case events.PostSearchEligibilityChanged:
		return c.applyEligibility(ctx, envelope)
	case events.PostDeleted:
		return c.applyDeleted(ctx, envelope)
	case events.PostRestored:
		return c.applyRestored(ctx, envelope)
	default:
		return nil
	}
}

// applyCreated projects PostCreated. The kind goes through the one
// canonical mapping; a value that is not a monetizable kind is stored
// as 'unknown' and the raw value is quarantined in
// content_type_exclusions, so nothing downstream can mistake a text
// post or a poll for a video kind and nothing is lost for the audit.
func (c *ContentOwnershipConsumer) applyCreated(ctx context.Context, envelope events.EventEnvelope) error {
	var payload events.PostCreatedPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return permanentOwnershipError{err}
	}
	contentID, creatorID, err := parseIDs(events.PostCreated, payload.PostID, payload.AuthorID)
	if err != nil {
		return err
	}
	createdAt := payload.CreatedAt
	if createdAt.IsZero() {
		createdAt = envelope.OccurredAt
	}
	if createdAt.IsZero() {
		return permanentOwnershipError{errors.New("PostCreated has no creation time")}
	}
	contentType, err := c.canonicalOrQuarantined(ctx, contentID, creatorID, payload.ContentType, events.PostCreated)
	if err != nil {
		return err
	}
	return c.store.UpsertContentOwnership(ctx, pgstore.ContentOwnership{
		ContentID: contentID, CreatorID: creatorID,
		ContentType: contentType, CreatedAt: createdAt,
	})
}

// canonicalOrQuarantined resolves the kind the ownership row will carry.
// Recording the exclusion is a durable write and happens before the row
// is written, so a retry after a failure repeats both idempotently.
func (c *ContentOwnershipConsumer) canonicalOrQuarantined(ctx context.Context, contentID, creatorID uuid.UUID, raw, source string) (string, error) {
	if canonical, ok := postclassify.CanonicalMonetizationType(raw); ok {
		return canonical, nil
	}
	if err := c.store.RecordContentTypeExclusion(ctx, pgstore.ContentTypeExclusion{
		ContentID: contentID, CreatorID: creatorID, RawContentType: raw,
		Reason: "not a monetizable kind", Source: source,
	}); err != nil {
		return "", err
	}
	slog.Info("content kind quarantined as unknown",
		"content_id", contentID.String(), "raw_content_type", raw, "source", source)
	return unknownContentType, nil
}

// applyReclassification keeps the ownership projection's content_type in
// step with post-service after the transcode pipeline reclassifies a
// video. shared/postclassify is the one rule that decides flick vs
// long_video; post-service applies it and announces the result, and this
// is analytics' side of that contract.
//
// It matters twice over, because content_type is not a label here:
// model.IsDisplayView picks the display-view bar from it, and
// monetization-service resolves the per-content-type RPM rate from the
// aggregates it ends up on — a flick and a long_video are 300 and 5000
// paise per 1000 views respectively. A post that uploaded as a plain
// video and turned out to be a flick would otherwise be counted and paid
// as long-form for the rest of its life.
//
// A missing row is permanent, not retryable: PostCreated is produced to
// the same topic under the same author key, so it has already been seen.
// Nothing to update means the create was rejected (an ownership
// conflict), and replaying this record forever would stall the partition.
func (c *ContentOwnershipConsumer) applyReclassification(ctx context.Context, envelope events.EventEnvelope) error {
	var payload events.PostContentTypeChangedPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return permanentOwnershipError{err}
	}
	contentID, creatorID, err := parseIDs(events.PostContentTypeChanged, payload.PostID, payload.AuthorID)
	if err != nil {
		return err
	}
	if payload.NewType == "" {
		return permanentOwnershipError{errors.New("PostContentTypeChanged has no new_type")}
	}
	newType, err := c.canonicalOrQuarantined(ctx, contentID, creatorID, payload.NewType, events.PostContentTypeChanged)
	if err != nil {
		return err
	}
	applied, err := c.store.UpdateContentType(ctx, contentID, creatorID, newType)
	if err != nil {
		return err
	}
	if !applied {
		slog.Warn("reclassification has no ownership row to correct",
			"content_id", contentID.String(), "new_type", newType)
		return nil
	}
	if newType != unknownContentType {
		if err := c.store.ClearContentTypeExclusion(ctx, contentID); err != nil {
			return err
		}
	}
	slog.Info("ownership content_type reclassified",
		"content_id", contentID.String(),
		"old_type", payload.OldType, "new_type", newType)
	return nil
}

// applyEligibility projects PostSearchEligibilityChanged, the declared
// single contract for every review, visibility, takedown and deletion
// change. The verdict is events.SearchEligible — the same rule search
// applies — with scheduled posts ineligible until they publish.
//
// The revision gate is in the store (atomic); this is the authoritative
// event, so an equal revision re-applies idempotently and can correct a
// same-revision PostRestored that landed first. A zero revision is
// unusable: it is honoured only when the event says deleted, otherwise
// the record fails closed — nothing is granted and it is committed past.
func (c *ContentOwnershipConsumer) applyEligibility(ctx context.Context, envelope events.EventEnvelope) error {
	var payload events.PostSearchEligibilityChangedPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return permanentOwnershipError{err}
	}
	contentID, creatorID, err := parseIDs(events.PostSearchEligibilityChanged, payload.PostID, payload.AuthorID)
	if err != nil {
		return err
	}
	if payload.SearchRev <= 0 && !payload.Deleted {
		slog.Warn("eligibility event has no usable revision; failing closed",
			"content_id", contentID.String(), "visibility", payload.Visibility, "review_status", payload.ReviewStatus)
		return permanentOwnershipError{errors.New("PostSearchEligibilityChanged has no search_rev")}
	}
	state := pgstore.EligibilityIneligible
	switch {
	case payload.Deleted:
		state = pgstore.EligibilityDeleted
	case events.SearchEligible(payload.Visibility, payload.ReviewStatus, false) && !payload.Scheduled:
		state = pgstore.EligibilityEligible
	}
	return c.writeEligibility(ctx, pgstore.ContentEligibility{
		ContentID: contentID, CreatorID: creatorID, State: state,
		EffectiveFrom: effectiveAt(payload.ChangedAt, envelope.OccurredAt),
		Rev:           payload.SearchRev,
		AllowEqualRev: true,
		IgnoreRev:     payload.SearchRev <= 0 && payload.Deleted,
	})
}

// applyDeleted is belt and braces for the soft delete. An old producer
// may omit search_rev; a delete is the one change safe to honour
// without one, because it only ever removes eligibility.
func (c *ContentOwnershipConsumer) applyDeleted(ctx context.Context, envelope events.EventEnvelope) error {
	var payload events.PostDeletedPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return permanentOwnershipError{err}
	}
	contentID, creatorID, err := parseIDs(events.PostDeleted, payload.PostID, payload.AuthorID)
	if err != nil {
		return err
	}
	return c.writeEligibility(ctx, pgstore.ContentEligibility{
		ContentID: contentID, CreatorID: creatorID, State: pgstore.EligibilityDeleted,
		EffectiveFrom: effectiveAt(payload.DeletedAt, envelope.OccurredAt),
		Rev:           payload.SearchRev,
		IgnoreRev:     payload.SearchRev <= 0,
	})
}

// applyRestored never grants eligibility on its own: a restored post is
// ineligible until the PostSearchEligibilityChanged emitted in the same
// transaction (same revision) says what its review and visibility make
// it. A restore with no revision cannot be gated and is dropped; the
// authoritative event carries one.
func (c *ContentOwnershipConsumer) applyRestored(ctx context.Context, envelope events.EventEnvelope) error {
	var payload events.PostRestoredPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return permanentOwnershipError{err}
	}
	contentID, creatorID, err := parseIDs(events.PostRestored, payload.PostID, payload.AuthorID)
	if err != nil {
		return err
	}
	if payload.SearchRev <= 0 {
		slog.Warn("PostRestored has no search_rev; leaving eligibility to the authoritative event",
			"content_id", contentID.String())
		return permanentOwnershipError{errors.New("PostRestored has no search_rev")}
	}
	return c.writeEligibility(ctx, pgstore.ContentEligibility{
		ContentID: contentID, CreatorID: creatorID, State: pgstore.EligibilityIneligible,
		EffectiveFrom: effectiveAt(payload.RestoredAt, envelope.OccurredAt),
		Rev:           payload.SearchRev,
	})
}

func (c *ContentOwnershipConsumer) writeEligibility(ctx context.Context, e pgstore.ContentEligibility) error {
	applied, err := c.store.ApplyContentEligibility(ctx, e)
	if err != nil {
		return err
	}
	if !applied {
		// Either the revision did not advance (a late delivery) or there
		// is no ownership row for this content and creator. Neither is a
		// reason to stall the partition.
		slog.Info("eligibility not applied: stale revision or no ownership row",
			"content_id", e.ContentID.String(), "state", e.State, "rev", e.Rev)
		return nil
	}
	slog.Info("content eligibility projected",
		"content_id", e.ContentID.String(), "state", e.State, "rev", e.Rev,
		"effective_from", e.EffectiveFrom.UTC().Format(time.RFC3339))
	return nil
}

func parseIDs(eventType, postID, authorID string) (contentID, creatorID uuid.UUID, err error) {
	contentID, err = uuid.Parse(postID)
	if err != nil || contentID == uuid.Nil {
		return uuid.Nil, uuid.Nil, permanentOwnershipError{fmt.Errorf("%s has invalid post_id", eventType)}
	}
	creatorID, err = uuid.Parse(authorID)
	if err != nil || creatorID == uuid.Nil {
		return uuid.Nil, uuid.Nil, permanentOwnershipError{fmt.Errorf("%s has invalid author_id", eventType)}
	}
	return contentID, creatorID, nil
}

// effectiveAt is the moment an eligibility change takes effect: the
// producer's timestamp, else the envelope's, else now. Never zero, so
// the rollup's date comparison always has a date to compare.
func effectiveAt(changedAt, occurredAt time.Time) time.Time {
	switch {
	case !changedAt.IsZero():
		return changedAt.UTC()
	case !occurredAt.IsZero():
		return occurredAt.UTC()
	}
	return time.Now().UTC()
}

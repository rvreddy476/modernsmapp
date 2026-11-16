package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

type Producer struct {
	writer *kafka.Writer
	// relay is the outbox relay's own writer (PublishOutbox): same brokers
	// and topic, but partitioned by the message key, so every event of one
	// aggregate lands on one partition in the order the relay wrote it. The
	// direct publishes about a user keep their own writer and its balancer;
	// the direct publishes about one post go through this writer too, keyed
	// by the post (publishForPost).
	relay *kafka.Writer
	// send writes one message through a writer; nil means the writer's own
	// WriteMessages. Tests set it to see which writer and key a publish used.
	send func(ctx context.Context, w *kafka.Writer, msg kafka.Message) error
}

func NewProducer(brokers []string, topic string) *Producer {
	return NewProducerWithDialer(brokers, topic, nil)
}

func NewProducerWithDialer(brokers []string, topic string, dialer *kafka.Dialer) *Producer {
	w := kafka.NewWriter(kafka.WriterConfig{
		Brokers:  brokers,
		Topic:    topic,
		Balancer: &kafka.LeastBytes{},
		Dialer:   dialer,
	})
	relay := kafka.NewWriter(kafka.WriterConfig{
		Brokers:      brokers,
		Topic:        topic,
		Balancer:     &kafka.Hash{},
		RequiredAcks: int(kafka.RequireAll),
		// The relay writes one message at a time and waits for it, under the
		// relay lock; the default one-second batch window would be a second
		// of waiting per outbox row.
		BatchTimeout: 10 * time.Millisecond,
		Dialer:       dialer,
	})
	return &Producer{writer: w, relay: relay}
}

// PublishPostCreated publishes a PostCreated event directly (not via the
// outbox). Module 2 M2-P0-1: reviewStatus is REQUIRED and must be the
// canonical persisted value of the row — search treats an empty value as
// ineligible, so passing "" silently makes the post unsearchable.
//
// createdAt must be the ROW'S OWN posts.created_at, and is a parameter for
// exactly that reason: this function used to stamp time.Now() itself, which
// no caller could override and every caller was silently wrong about.
//
// It is not cosmetic. PostCreated is the only thing that writes
// analytics.content_ownership, and that row's created_at is the only record
// of the post's creation date that crosses the service boundary; nothing
// downstream can recover the true value if the event carries a fabricated
// one. It is not, however, what earnings are dated by: the creator fund
// reads analytics.content_daily_summary and windows on day_bucket — the day
// the views happened — and its 90-day eligibility scan reads the same table,
// so a misdated ownership row moves no money by itself. What does see it is
// whatever walks content_ownership by creator and created_at, which is what
// idx_content_ownership_creator is for.
//
// The outbox paths already did this correctly — buildPostCreatedPayload
// (schedule.go) and the thread path both read p.CreatedAt — so this was the
// one path that invented a timestamp, and the backfill tool was more accurate
// than production as a result.
func (p *Producer) PublishPostCreated(ctx context.Context, postID, authorID uuid.UUID, text, visibility, contentType, reviewStatus string, durationSeconds int, createdAt time.Time) error {
	// A zero createdAt would serialise as year 1 and quietly park the content
	// outside every window that walks content_ownership by created_at.
	// Refusing is better than a bad date nothing downstream can detect.
	if createdAt.IsZero() {
		return fmt.Errorf("PublishPostCreated: createdAt is required for post %s", postID)
	}
	payload := events.PostCreatedPayload{
		PostID:          postID.String(),
		AuthorID:        authorID.String(),
		Text:            text,
		Visibility:      visibility,
		ContentType:     contentType,
		DurationSeconds: durationSeconds,
		CreatedAt:       createdAt,
		ReviewStatus:    reviewStatus,
		SearchRev:       1,
	}
	return p.publishForPost(ctx, events.PostCreated, &authorID, postID, payload)
}

// PublishPostContentTypeChanged is fired by the MediaTranscodeConsumer
// after a post is reclassified (typically long_video → flick once
// transcode reveals the real duration + dimensions). feed-service
// consumes this and rewrites the matching content_type column on
// home_timeline_by_user + author_timeline_by_author so feed queries
// that filter on content_type don't return stale results.
func (p *Producer) PublishPostContentTypeChanged(ctx context.Context, postID, authorID uuid.UUID, oldType, newType string) error {
	payload := events.PostContentTypeChangedPayload{
		PostID:    postID.String(),
		AuthorID:  authorID.String(),
		OldType:   oldType,
		NewType:   newType,
		ChangedAt: time.Now(),
	}
	return p.publishForPost(ctx, events.PostContentTypeChanged, &authorID, postID, payload)
}

func (p *Producer) PublishPostReacted(ctx context.Context, postID, postAuthorID, reactorID uuid.UUID, reactType string) error {
	payload := events.PostReactedPayload{
		PostID:       postID.String(),
		PostAuthorID: postAuthorID.String(),
		ReactorID:    reactorID.String(),
		ReactType:    reactType,
		CreatedAt:    time.Now(),
	}
	return p.publishForPost(ctx, events.PostReacted, &reactorID, postID, payload)
}

func (p *Producer) PublishCommentReacted(ctx context.Context, commentID, postID, commentAuthorID, reactorID uuid.UUID, reactType string) error {
	payload := events.CommentReactedPayload{
		CommentID:       commentID.String(),
		PostID:          postID.String(),
		CommentAuthorID: commentAuthorID.String(),
		ReactorID:       reactorID.String(),
		ReactType:       reactType,
		CreatedAt:       time.Now(),
	}
	return p.publish(ctx, events.CommentReacted, &reactorID, payload)
}

func (p *Producer) PublishCommentCreated(ctx context.Context, commentID, postID, postAuthorID, authorID uuid.UUID, text string) error {
	payload := events.CommentCreatedPayload{
		CommentID:    commentID.String(),
		PostID:       postID.String(),
		PostAuthorID: postAuthorID.String(),
		AuthorID:     authorID.String(),
		Text:         text,
		CreatedAt:    time.Now(),
	}
	return p.publishForPost(ctx, events.CommentCreated, &authorID, postID, payload)
}

func (p *Producer) PublishSpamDetected(ctx context.Context, userID uuid.UUID, reason string, score float64) error {
	payload := events.SpamDetectedPayload{
		UserID: userID.String(),
		Reason: reason,
		Score:  score,
	}
	return p.publish(ctx, events.EventSpamDetected, &userID, payload)
}

func (p *Producer) PublishStoryCreated(ctx context.Context, storyID, authorID uuid.UUID, mediaType string) error {
	payload := events.StoryCreatedPayload{
		StoryID:   storyID.String(),
		AuthorID:  authorID.String(),
		MediaType: mediaType,
		CreatedAt: time.Now(),
	}
	return p.publish(ctx, events.StoryCreated, &authorID, payload)
}

// PublishUserMentioned emits a user.mentioned event for a @mention in a post.
func (p *Producer) PublishUserMentioned(ctx context.Context, mentionedUserID, authorID uuid.UUID, postID string) error {
	payload := UserMentionedPayload(mentionedUserID, authorID, postID, uuid.Nil, time.Now())
	return p.publish(ctx, events.EventUserMentioned, &authorID, payload)
}

// PublishUserMentionedInComment emits user.mentioned for a @mention in a
// comment or reply (T-12). Same event as a post mention, with comment_id set:
// notification-service deep-links it to /post/<post_id>?focusComment=<id>,
// the link every other comment notification uses.
func (p *Producer) PublishUserMentionedInComment(ctx context.Context, mentionedUserID, authorID, postID, commentID uuid.UUID) error {
	payload := UserMentionedPayload(mentionedUserID, authorID, postID.String(), commentID, time.Now())
	return p.publish(ctx, events.EventUserMentioned, &authorID, payload)
}

// UserMentionedPayload builds the user.mentioned payload. commentID is
// uuid.Nil for a mention in the post itself, which leaves comment_id off the
// wire (omitempty) exactly as before.
func UserMentionedPayload(mentionedUserID, authorID uuid.UUID, postID string, commentID uuid.UUID, at time.Time) events.UserMentionedPayload {
	payload := events.UserMentionedPayload{
		MentionedUserID: mentionedUserID.String(),
		AuthorID:        authorID.String(),
		PostID:          postID,
		OccurredAt:      at,
	}
	if commentID != uuid.Nil {
		payload.CommentID = commentID.String()
	}
	return payload
}

// UserMentionedMessage is the Kafka message PublishUserMentioned writes for
// a post mention: the same envelope, key and payload, without the broker.
// It exists so the wire contract notification-service consumes can be pinned
// in a test (testdata/contracts/user_mentioned_post.json).
func UserMentionedMessage(ctx context.Context, mentionedUserID, authorID uuid.UUID, postID string, at time.Time) (kafka.Message, error) {
	return encodeMessage(ctx, events.EventUserMentioned, &authorID, UserMentionedPayload(mentionedUserID, authorID, postID, uuid.Nil, at))
}

func (p *Producer) publish(ctx context.Context, eventType string, actorID *uuid.UUID, payload interface{}) error {
	msg, err := encodeMessage(ctx, eventType, actorID, payload)
	if err != nil {
		return err
	}
	return p.write(ctx, p.writer, msg)
}

func (p *Producer) write(ctx context.Context, w *kafka.Writer, msg kafka.Message) error {
	if p.send != nil {
		return p.send(ctx, w, msg)
	}
	return w.WriteMessages(ctx, msg)
}

/*
	publishForPost is the direct (non-outbox) publish of an event about one
	post (feed-07 leftover).

	These used to go out like every other direct publish: keyed by the actor,
	on a writer whose balancer ignores the key. The outbox relay writes a
	post's lifecycle events (created, deleted, restored, visibility) keyed by
	the post id through the key-hashing writer, so a direct event about the
	same post sat on some other partition and a consumer could apply it on
	either side of them: a crosspost's PostCreated after its PostDeleted, a
	repost's undo before the repost.

	Now the key is the post id and the writer is the relay's, so a post's
	direct events share its partition with its outbox events and are consumed
	in the order the brokers took them. The envelope is unchanged (the actor
	is still on it); no consumer reads the key except to copy it onto a
	dead-letter message.

	What this does not do: a direct publish happens at request time and an
	outbox row waits for the relay, so a direct event can still reach the
	partition ahead of an older outbox row that has not been relayed yet.
	Closing that needs these events written to the outbox (feed-29).
*/
func (p *Producer) publishForPost(ctx context.Context, eventType string, actorID *uuid.UUID, postID uuid.UUID, payload interface{}) error {
	msg, err := encodePostMessage(ctx, eventType, actorID, postID, payload)
	if err != nil {
		return err
	}
	w := p.relay
	if w == nil { // a Producer built without the relay writer
		w = p.writer
	}
	return p.write(ctx, w, msg)
}

// encodePostMessage is encodeMessage with the post id as the partition key.
// A post id that is not known (uuid.Nil) keeps encodeMessage's key rather
// than piling every such event on one partition.
func encodePostMessage(ctx context.Context, eventType string, actorID *uuid.UUID, postID uuid.UUID, payload interface{}) (kafka.Message, error) {
	msg, err := encodeMessage(ctx, eventType, actorID, payload)
	if err != nil {
		return kafka.Message{}, err
	}
	if postID != uuid.Nil {
		msg.Key = []byte(postID.String())
	}
	return msg, nil
}

// encodeMessage builds the envelope and partition key every direct publish
// writes.
func encodeMessage(ctx context.Context, eventType string, actorID *uuid.UUID, payload interface{}) (kafka.Message, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return kafka.Message{}, fmt.Errorf("failed to marshal payload: %w", err)
	}

	var actorStr *string
	if actorID != nil {
		s := actorID.String()
		actorStr = &s
	}

	envelope := events.NewEnvelope(ctx, eventType, actorStr, payloadBytes)

	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		return kafka.Message{}, fmt.Errorf("failed to marshal envelope: %w", err)
	}

	// Partition key: prefer actorID so all events for the same user
	// land on the same partition and stay ordered (PostCreated →
	// PostUpdated → PostDeleted are processed in order by feed-service).
	// Falls back to EventID for actorless events. Previously every
	// event used a fresh EventID, randomising partitions and breaking
	// per-user ordering guarantees consumers relied on.
	key := []byte(envelope.EventID)
	if actorStr != nil {
		key = []byte(*actorStr)
	}
	return kafka.Message{Key: key, Value: envelopeBytes}, nil
}

// OutboxMessage is the Kafka message the outbox relay writes for one
// post_outbox_events row (feed-07).
//
// The event id is the row's own id, so a row that is published again (a
// crash or a failed mark-published between the write and the UPDATE) carries
// the id it carried the first time, and a consumer that dedupes on event_id
// (shared/kafka) recognises it. It used to be a fresh uuid per publish.
//
// The key is the aggregate id (the post id on every post event), so with the
// relay's key-hashing writer all events of one post share a partition and
// are consumed in the order they were written: a delete can no longer
// overtake the restore that followed it. It used to be the fresh event id.
// A row with no aggregate id is keyed by its own id.
func OutboxMessage(ctx context.Context, rowID uuid.UUID, eventType string, aggregateID uuid.UUID, payload json.RawMessage) (kafka.Message, error) {
	envelope := events.NewEnvelope(ctx, eventType, nil, payload)
	envelope.EventID = rowID.String()
	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		return kafka.Message{}, fmt.Errorf("failed to marshal envelope: %w", err)
	}
	key := aggregateID
	if key == uuid.Nil {
		key = rowID
	}
	return kafka.Message{Key: []byte(key.String()), Value: envelopeBytes}, nil
}

// PublishOutbox writes one OutboxMessage through the relay's key-hashing
// writer and returns once the brokers have acknowledged it.
func (p *Producer) PublishOutbox(ctx context.Context, msg kafka.Message) error {
	if p.relay == nil {
		return fmt.Errorf("outbox relay writer is not configured")
	}
	return p.write(ctx, p.relay, msg)
}

// PublishUploadDeleted emits an upload.deleted event when a user deletes their upload.
func (p *Producer) PublishUploadDeleted(ctx context.Context, postID, authorID uuid.UUID, contentType string) error {
	payload := events.UploadDeletedPayload{
		PostID:      postID.String(),
		AuthorID:    authorID.String(),
		ContentType: contentType,
		DeletedAt:   time.Now(),
	}
	return p.publishForPost(ctx, events.UploadDeleted, &authorID, postID, payload)
}

// PublishCrosspostRemoved emits a crosspost.removed event.
func (p *Producer) PublishCrosspostRemoved(ctx context.Context, crosspostID, sourcePostID uuid.UUID, sourceModule string, targetPostID uuid.UUID) error {
	payload := events.CrosspostRemovedPayload{
		CrosspostID:  crosspostID.String(),
		SourcePostID: sourcePostID.String(),
		SourceModule: sourceModule,
		TargetPostID: targetPostID.String(),
		RemovedAt:    time.Now(),
	}
	return p.publishForPost(ctx, events.CrosspostRemoved, nil, targetPostID, payload)
}

// PublishPostReposted emits a post.reposted event when a user reposts a post.
func (p *Producer) PublishPostReposted(ctx context.Context, repostID, reposterID, originalPostID, originalAuthorID uuid.UUID, repostType, quoteText, visibility, sourceCtxType, sourceCtxID string) error {
	payload := events.PostRepostedPayload{
		RepostID:          repostID.String(),
		ReposterUserID:    reposterID.String(),
		OriginalPostID:    originalPostID.String(),
		OriginalAuthorID:  originalAuthorID.String(),
		RepostType:        repostType,
		QuoteText:         quoteText,
		Visibility:        visibility,
		SourceContextType: sourceCtxType,
		SourceContextID:   sourceCtxID,
		CreatedAt:         time.Now(),
	}
	return p.publishForPost(ctx, events.EventPostReposted, &reposterID, originalPostID, payload)
}

// PublishPostRepostUndone emits a post.repost_undone event when a user undoes a repost.
func (p *Producer) PublishPostRepostUndone(ctx context.Context, repostID, reposterID, originalPostID, originalAuthorID uuid.UUID, repostType string) error {
	payload := events.PostRepostUndonePayload{
		RepostID:         repostID.String(),
		ReposterUserID:   reposterID.String(),
		OriginalPostID:   originalPostID.String(),
		OriginalAuthorID: originalAuthorID.String(),
		RepostType:       repostType,
		UndoneAt:         time.Now(),
	}
	return p.publishForPost(ctx, events.EventPostRepostUndone, &reposterID, originalPostID, payload)
}

func (p *Producer) Close() error {
	err := p.writer.Close()
	if p.relay != nil {
		if relayErr := p.relay.Close(); err == nil {
			err = relayErr
		}
	}
	return err
}

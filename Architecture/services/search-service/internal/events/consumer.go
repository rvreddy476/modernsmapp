package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/atpost/search-service/internal/privacyclient"
	"github.com/atpost/search-service/internal/purge"
	"github.com/atpost/search-service/internal/store/search"
	"github.com/atpost/shared/events"
	"github.com/segmentio/kafka-go"
)

// hashtagRegex matches #word patterns in text (word chars and underscores).
var hashtagRegex = regexp.MustCompile(`#(\w+)`)

// extractHashtags parses all #hashtag occurrences from text and returns
// lowercase deduplicated hashtag strings (without the leading #).
func extractHashtags(text string) []string {
	matches := hashtagRegex.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(matches))
	tags := make([]string, 0, len(matches))
	for _, m := range matches {
		tag := strings.ToLower(m[1])
		if _, ok := seen[tag]; !ok {
			seen[tag] = struct{}{}
			tags = append(tags, tag)
		}
	}
	return tags
}

type Consumer struct {
	reader   *kafka.Reader
	store    *search.Store
	dlq      *kafka.Writer // optional; nil = log-only on failure
	dlqTopic string
	groupID  string
	topic    string
	retry    retryPolicy
	// privacy resolves an author's account_visibility at index time
	// (private accounts). Nil means unconfigured: documents index as
	// public and a startup warning is logged — acceptable only on a dev
	// rig without the identity user-service.
	privacy privacyclient.Lookup
	// lifecycle handles user.deactivated / deletion_scheduled / reactivated
	// / deletion_cancelled / purge_requested (see internal/purge). Wired
	// onto the identity-topic consumer only. Optional.
	lifecycle *purge.Handler
}

// WithPrivacyLookup wires the identity settings lookup used to stamp
// is_private / author_is_private on every (re)indexed document.
func (c *Consumer) WithPrivacyLookup(l privacyclient.Lookup) *Consumer {
	c.privacy = l
	return c
}

// WithLifecycleHandler wires the account-control (hide / purge) handler.
func (c *Consumer) WithLifecycleHandler(h *purge.Handler) *Consumer {
	c.lifecycle = h
	return c
}

// authorIsPrivate resolves the flag for one user. A lookup FAILURE is an
// error, which makes the message retry (and eventually dead-letter) rather
// than indexing a private account's post as public — the durable-outcome
// rule this consumer already applies to every other write.
func (c *Consumer) authorIsPrivate(ctx context.Context, userID string) (bool, error) {
	if c.privacy == nil {
		return false, nil
	}
	private, err := c.privacy.IsPrivate(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("resolve account_visibility for %s: %w", userID, err)
	}
	return private, nil
}

func NewConsumer(brokers []string, groupID string, topic string, store *search.Store) *Consumer {
	return NewConsumerWithDialer(brokers, groupID, topic, store, nil)
}

func NewConsumerWithDialer(brokers []string, groupID string, topic string, store *search.Store, dialer *kafka.Dialer) *Consumer {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		GroupID:  groupID,
		Topic:    topic,
		MinBytes: 10e3,
		MaxBytes: 10e6,
		Dialer:   dialer,
	})

	// Audit HS2: configure a DLQ writer so failed messages don't fall
	// silently off the back of the indexer. Topic is env-tunable;
	// empty disables DLQ (the default for unit tests).
	dlqTopic := os.Getenv("SEARCH_DLQ_TOPIC")
	if dlqTopic == "" {
		dlqTopic = "search.events.v1.dlq"
	}
	var dlqWriter *kafka.Writer
	if dlqTopic != "-" { // "-" explicitly disables
		dlqWriter = &kafka.Writer{
			Addr:     kafka.TCP(brokers...),
			Topic:    dlqTopic,
			Balancer: &kafka.Hash{},
		}
		if dialer != nil {
			dlqWriter.Transport = &kafka.Transport{Dial: dialer.DialFunc}
		}
	}

	return &Consumer{
		reader:   reader,
		store:    store,
		dlq:      dlqWriter,
		dlqTopic: dlqTopic,
		groupID:  groupID,
		topic:    topic,
		retry:    defaultRetryPolicy(),
	}
}

// Start consumes the topic until ctx is cancelled.
//
// M2-P0-3: this uses FetchMessage + explicit CommitMessages, NOT
// ReadMessage. With a consumer group, kafka-go's ReadMessage commits the
// offset before returning the message to the caller, so the previous loop
// was committing every message before it had been processed. The comment
// claiming shutdown left the offset uncommitted was simply wrong.
//
// The consequences were real losses, not theoretical ones: if the process
// exited between ReadMessage and the projection write, or the retries
// exhausted and the DLQ write then failed, the message was already
// committed and never redelivered. For a takedown or a visibility
// downgrade that means the content stays publicly searchable forever,
// with nothing left in the system that would ever fix it.
//
// The offset is now committed only after the projection has been applied,
// or after the message is durably handed off to the DLQ.
//
// RE-REVIEW P0-1 — NEVER FETCH PAST AN UNRESOLVED MESSAGE.
//
// Switching to FetchMessage was necessary but not sufficient. The loop
// used to `continue` when both processing and the DLQ handoff failed,
// which fetched the NEXT message from the same partition. Kafka offsets
// are cumulative: committing offset N+1 implicitly commits N. So one
// later success silently committed the failed removal too, and it was
// never redelivered — the exact loss the FetchMessage change was meant
// to prevent, reintroduced one line below it.
//
// The loop now blocks on the current message until it reaches a durable
// outcome. Blocking a partition is a real cost: this consumer stops
// making progress on that partition while it retries. That is the correct
// trade for a stream carrying moderation decisions — falling behind is
// visible and recoverable, dropping a takedown is neither.
func (c *Consumer) Start(ctx context.Context) {
	for {
		m, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				slog.Info("search consumer shutting down")
				return
			}
			slog.Error("kafka consumer error", "error", err)
			time.Sleep(2 * time.Second)
			continue
		}

		if !c.handleUntilDurable(ctx, m) {
			// Shutdown, or the message could not be resolved. Either way
			// the offset stays put and the message is redelivered.
			return
		}

		if err := c.commit(ctx, m); err != nil {
			// The projection is already applied and idempotent, so a
			// failed commit costs a redelivery, not correctness.
			slog.Error("search: offset commit failed; message will be redelivered",
				"topic", m.Topic, "offset", m.Offset, "error", err)
		}
	}
}

// handleUntilDurable processes one message, retrying in place until it
// either succeeds or is durably dead-lettered. Reports whether the offset
// may now advance.
//
// Returning false means "stop the loop entirely" rather than "skip this
// message" — skipping is what caused the leapfrog.
func (c *Consumer) handleUntilDurable(ctx context.Context, m kafka.Message) bool {
	// Escalating pause between outer attempts. The inner retry ladder
	// already covers short blips; this covers an outage long enough that
	// the DLQ is unreachable too.
	stall := 2 * time.Second
	const maxStall = 60 * time.Second

	for {
		// M2-P0-2: retry in-process before dead-lettering. A transient
		// OpenSearch failure must not be the reason a takedown or a
		// visibility downgrade never reaches the index.
		procErr := c.retry.retry(ctx, func() error { return c.processMessage(ctx, m) })
		if procErr == nil {
			return true
		}
		if ctx.Err() != nil {
			// Shutting down mid-message. Do NOT commit — redelivery after
			// restart is exactly what we want.
			slog.Info("search consumer shutting down mid-message", "offset", m.Offset)
			return false
		}

		slog.Error("failed to process message after retries",
			"topic", m.Topic, "offset", m.Offset, "error", procErr)
		IndexOpFailures.WithLabelValues(eventTypeOf(m)).Inc()

		// A CONFIRMED durable handoff is the only thing that lets the
		// offset advance.
		if c.sendToDLQ(ctx, m, procErr) {
			return true
		}

		slog.Error("search: DLQ handoff failed; holding the partition rather than "+
			"advancing past an unresolved message",
			"topic", m.Topic, "offset", m.Offset, "retry_in", stall)

		select {
		case <-ctx.Done():
			return false
		case <-time.After(stall):
		}
		if stall < maxStall {
			stall *= 2
			if stall > maxStall {
				stall = maxStall
			}
		}
	}
}

// commit advances the consumer group past a message that has been fully
// handled. It uses a background context so a cancelled request context
// cannot abandon a commit for work that actually succeeded.
func (c *Consumer) commit(ctx context.Context, m kafka.Message) error {
	commitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	return c.reader.CommitMessages(commitCtx, m)
}

// sendToDLQ writes a failed message to the DLQ topic and reports whether
// the handoff is DURABLE.
//
// M2-P0-3: the return value matters. This used to be best-effort with no
// result, and the caller committed the source offset regardless — so a
// failed DLQ write meant the message was gone from both the source topic
// and the DLQ. The caller now refuses to commit unless this returns true.
//
// When the DLQ is disabled (unit tests, or SEARCH_DLQ_TOPIC="-") there is
// no durable destination, so this reports false and the message stays
// uncommitted rather than being silently discarded.
func (c *Consumer) sendToDLQ(ctx context.Context, m kafka.Message, processErr error) bool {
	if c.dlq == nil {
		return false
	}
	dlqCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	headers := append(m.Headers,
		kafka.Header{Key: "x-dlq-error", Value: []byte(processErr.Error())},
		kafka.Header{Key: "x-dlq-original-topic", Value: []byte(c.topic)},
		kafka.Header{Key: "x-dlq-consumer-group", Value: []byte(c.groupID)},
	)
	if err := c.dlq.WriteMessages(dlqCtx, kafka.Message{
		Key:     m.Key,
		Value:   m.Value,
		Headers: headers,
	}); err != nil {
		slog.Error("search: DLQ write failed", "error", err, "dlq_topic", c.dlqTopic)
		return false
	}
	slog.Warn("search: message routed to DLQ", "dlq_topic", c.dlqTopic, "offset", m.Offset)
	return true
}

func (c *Consumer) processMessage(ctx context.Context, m kafka.Message) error {
	var envelope events.EventEnvelope
	if err := json.Unmarshal(m.Value, &envelope); err != nil {
		return err
	}

	switch envelope.EventType {
	case events.UserRegistered:
		var p events.UserRegisteredPayload
		if err := unmarshalPayload(envelope.Payload, &p); err != nil {
			return err
		}

		displayName := p.FirstName
		if p.LastName != "" {
			displayName += " " + p.LastName
		}
		if displayName == "" {
			displayName = "New User"
		}

		// IndexUser is a full-document replace, so every user write must
		// carry is_private or a profile edit would silently reset it.
		isPrivate, err := c.authorIsPrivate(ctx, p.UserID)
		if err != nil {
			return err
		}
		return c.store.IndexUser(ctx, search.UserDoc{
			UserID:      p.UserID,
			DisplayName: displayName,
			IsPrivate:   isPrivate,
		})

	case events.UserProfileUpdated:
		var p events.UserProfileUpdatedPayload
		if err := unmarshalPayload(envelope.Payload, &p); err != nil {
			return err
		}

		isPrivate, err := c.authorIsPrivate(ctx, p.UserID)
		if err != nil {
			return err
		}
		return c.store.IndexUser(ctx, search.UserDoc{
			UserID:        p.UserID,
			Username:      p.Username,
			DisplayName:   p.DisplayName,
			Bio:           p.Bio,
			AvatarMediaID: p.AvatarMediaID,
			IsVerified:    p.IsVerified,
			IsPrivate:     isPrivate,
		})

	case events.UserSettingsChanged:
		// Private accounts: the identity user-service announces every
		// committed settings write with the NEW account_visibility. Flip
		// the user document's flag and rewrite author_is_private across
		// every post by that author in one update_by_query, so a "go
		// private" takes effect in search within the refresh interval.
		var p struct {
			UserID            string `json:"user_id"`
			AccountVisibility string `json:"account_visibility"`
		}
		if err := unmarshalPayload(envelope.Payload, &p); err != nil {
			return err
		}
		if p.UserID == "" {
			return nil // malformed; nothing addressable
		}
		var isPrivate bool
		switch strings.ToLower(p.AccountVisibility) {
		case "private":
			isPrivate = true
		case "public":
			isPrivate = false
		default:
			// Older producers omit the field: read the current value.
			if c.privacy != nil {
				c.privacy.Invalidate(p.UserID)
			}
			v, err := c.authorIsPrivate(ctx, p.UserID)
			if err != nil {
				return err
			}
			isPrivate = v
		}
		if primer, ok := c.privacy.(interface{ Prime(string, bool) }); ok && c.privacy != nil {
			primer.Prime(p.UserID, isPrivate)
		}
		if err := c.store.UpdateUserPrivacy(ctx, p.UserID, isPrivate); err != nil {
			return fmt.Errorf("settings_changed: update user %s privacy: %w", p.UserID, err)
		}
		if err := c.store.UpdatePostsAuthorPrivacy(ctx, p.UserID, isPrivate); err != nil {
			return fmt.Errorf("settings_changed: update posts by %s privacy: %w", p.UserID, err)
		}
		slog.Info("search: account visibility applied", "user_id", p.UserID, "is_private", isPrivate)
		return nil

	case events.PostCreated:
		var p events.PostCreatedPayload
		if err := unmarshalPayload(envelope.Payload, &p); err != nil {
			return err
		}

		// ── Module 2 M2-P0-1: the moderation gate ────────────────────
		//
		// Previously EVERY PostCreated was indexed immediately, with no
		// regard for moderation state. A post held at 'pending' by the
		// Module 1 video/voice safety gate — or 'flagged' by spam
		// detection — became publicly findable, and its text was
		// returned directly from the OpenSearch _source. That defeated
		// the Module 1 guarantee at the search boundary.
		//
		// FAIL CLOSED: empty, missing, malformed, pending, flagged,
		// rejected and needs_changes are all ineligible. An unknown
		// value is ineligible. There is deliberately no "legacy means
		// approved" path — Codex rejected it because replayed events and
		// partially-deployed producers would reopen the exposure.
		if !events.SearchEligible(p.Visibility, p.ReviewStatus, false) {
			slog.Debug("search: post not eligible for public index",
				"post_id", p.PostID, "visibility", p.Visibility,
				"review_status", p.ReviewStatus)
			// Not an error: this is the expected path for held content.
			// Nothing is indexed and NO derived signal is emitted — in
			// particular the hashtag counters below are skipped, so held
			// content cannot influence autocomplete or trending.
			return nil
		}

		rev := p.SearchRev
		if rev <= 0 {
			rev = 1 // creation baseline
		}

		// M2-P0-2: this used to be an unconditional IndexPost, which meant
		// creation ignored the revision barrier entirely. A replayed
		// PostCreated at rev 1 arriving after a rev-2 removal simply
		// overwrote the tombstone with a public approved document — no
		// concurrency required. It now goes through the same atomic
		// compare-and-apply as every other write.
		//
		// M2-P0-7 / re-review P0-2: IndexPostUnlessAuthorErased wraps that
		// with a fence check AND a recheck, so an account erased while
		// this write is in flight cannot end up with a surviving post.
		authorPrivate, err := c.authorIsPrivate(ctx, p.AuthorID)
		if err != nil {
			return err
		}
		return c.store.IndexPostUnlessAuthorErased(ctx, search.PostProjection{
			PostID: p.PostID,
			Rev:    rev,
			Doc: search.PostDoc{
				PostID:          p.PostID,
				AuthorID:        p.AuthorID,
				AuthorIsPrivate: authorPrivate,
				Text:            p.Text,
				Hashtags:        extractHashtags(p.Text),
				Visibility:      p.Visibility,
				ReviewStatus:    p.ReviewStatus,
				SearchRev:       rev,
				CreatedAt:       p.CreatedAt,
			},
		})

	case events.PostSearchEligibilityChanged:
		// M2-P0-2: the single contract for approval, rejection, flagging,
		// needs-changes, takedown, visibility change, and deletion.
		var p events.PostSearchEligibilityChangedPayload
		if err := unmarshalPayload(envelope.Payload, &p); err != nil {
			return err
		}
		return c.applySearchEligibility(ctx, p)

	case events.ContentTakenDown:
		// M2-P0-2: an explicit takedown must leave the index even if the
		// eligibility event is delayed or lost. Removal is idempotent.
		var p events.ContentTakenDownPayload
		if err := unmarshalPayload(envelope.Payload, &p); err != nil {
			return err
		}
		if !strings.EqualFold(p.EntityType, "post") {
			return nil
		}
		// The takedown payload carries no revision. AutoRev has OpenSearch
		// stamp storedRev+1 inside the same locked update that performs
		// the removal, so the barrier is raised atomically — the previous
		// read-then-write version could be overtaken between the two.
		if err := c.store.ApplyPostProjection(ctx, search.PostProjection{
			PostID:  p.EntityID,
			AutoRev: true,
			Removed: true,
		}); err != nil {
			return fmt.Errorf("takedown: remove post %s from index: %w", p.EntityID, err)
		}
		slog.Info("search: post removed by takedown", "post_id", p.EntityID)
		return nil

	case events.PostReacted:
		// Engagement bump: +1 per like-like reaction, -1 on unreact is
		// indistinguishable from the event we receive (only the
		// add direction is published today), so we only +1. The hot
		// path is multiply-in-log1p so over-counting at +1/event has
		// asymptotically negligible impact on ranking.
		var p events.PostReactedPayload
		if err := unmarshalPayload(envelope.Payload, &p); err != nil {
			return err
		}
		return c.store.AddToEngagementScore(ctx, search.IndexPosts, p.PostID, 1)

	case events.CommentCreated:
		var p events.CommentCreatedPayload
		if err := unmarshalPayload(envelope.Payload, &p); err != nil {
			return err
		}
		// Posts weight: like=1, comment=2 (see mappings.computeEngagementScore).
		return c.store.AddToEngagementScore(ctx, search.IndexPosts, p.PostID, 2)

	case events.EventPostReposted:
		// Posts weight: share=3. We use the post repost event as the
		// share signal; ReelShared bumps a different post id below.
		var p struct {
			PostID string `json:"post_id"`
		}
		if err := unmarshalPayload(envelope.Payload, &p); err != nil {
			return err
		}
		if p.PostID != "" {
			return c.store.AddToEngagementScore(ctx, search.IndexPosts, p.PostID, 3)
		}
		return nil

	case events.UserFollowed:
		var p events.UserFollowedPayload
		if err := unmarshalPayload(envelope.Payload, &p); err != nil {
			return err
		}
		// Users weight: follower_count carries 1.0 — bump the followee.
		return c.store.AddToEngagementScore(ctx, search.IndexUsers, p.FolloweeID, 1)

	case events.UserUnfollowed:
		var p events.UserUnfollowedPayload
		if err := unmarshalPayload(envelope.Payload, &p); err != nil {
			return err
		}
		return c.store.AddToEngagementScore(ctx, search.IndexUsers, p.FolloweeID, -1)

	// ── Legacy delete events (M2-P0-2) ───────────────────────────────
	//
	// These three are still produced by current post-service paths and
	// used to call a hard DeletePost. A hard delete removes the document
	// AND the revision stored on it, so it erased the resurrection
	// barrier: any older approval or PostCreated still in flight would
	// then recreate the post as public and approved.
	//
	// They now write a removal marker with AutoRev, which removes the
	// content and RAISES the barrier in one atomic operation. That makes
	// them safe to receive in any order relative to the canonical
	// eligibility transition — arriving late is a no-op, arriving early
	// is superseded by the higher canonical revision.

	case events.PostDeleted:
		var p events.PostDeletedPayload
		if err := unmarshalPayload(envelope.Payload, &p); err != nil {
			return err
		}
		return c.store.ApplyPostProjection(ctx, search.PostProjection{
			PostID: p.PostID, AutoRev: true, Removed: true,
		})

	case events.EventUserDeletionRequested:
		var p events.UserDeletionRequestedPayload
		if err := unmarshalPayload(envelope.Payload, &p); err != nil {
			return err
		}
		// M2-P0-7: erase the author's content behind a permanent fence
		// instead of delete-by-query. Deleting was faster and strictly
		// less safe — it destroyed every revision marker at once, so any
		// stale event could recreate the erased account's posts.
		//
		// This error is returned, not logged and swallowed: a failed
		// erasure must be retried and dead-lettered, not forgotten.
		if err := c.store.EraseAuthorContent(ctx, p.UserID); err != nil {
			return fmt.Errorf("erase author content %s: %w", p.UserID, err)
		}
		return c.store.DeleteUser(ctx, p.UserID)

	case events.CrosspostRemoved:
		var p events.CrosspostRemovedPayload
		if err := unmarshalPayload(envelope.Payload, &p); err != nil {
			return err
		}
		return c.store.ApplyPostProjection(ctx, search.PostProjection{
			PostID: p.TargetPostID, AutoRev: true, Removed: true,
		})

	case events.UploadDeleted:
		var p events.UploadDeletedPayload
		if err := unmarshalPayload(envelope.Payload, &p); err != nil {
			return err
		}
		return c.store.ApplyPostProjection(ctx, search.PostProjection{
			PostID: p.PostID, AutoRev: true, Removed: true,
		})

	case events.HandleChanged:
		var p events.HandleChangedPayload
		if err := unmarshalPayload(envelope.Payload, &p); err != nil {
			return err
		}
		// Update the user's username in the search index via partial update
		return c.store.UpdateUserUsername(ctx, p.UserID, p.NewUsername)

	// --- Communities ---
	case events.EventCommunityCreated:
		var p communityCreatedPayload
		if err := unmarshalPayload(envelope.Payload, &p); err != nil {
			return err
		}
		return c.store.IndexCommunity(ctx, search.CommunityDoc{
			CommunityID:   p.CommunityID,
			OwnerID:       p.OwnerID,
			Name:          p.Name,
			CommunityType: p.CommunityType,
			CreatedAt:     p.CreatedAt,
		})

	case events.EventCommunityUpdated:
		var p communityUpdatedPayload
		if err := unmarshalPayload(envelope.Payload, &p); err != nil {
			return err
		}
		// Partial update; leaves counters intact.
		return c.store.IndexCommunity(ctx, search.CommunityDoc{
			CommunityID:   p.CommunityID,
			Name:          p.Name,
			CommunityType: p.CommunityType,
			CreatedAt:     p.UpdatedAt,
		})

	case events.EventCommunityDeleted:
		var p communityDeletedPayload
		if err := unmarshalPayload(envelope.Payload, &p); err != nil {
			return err
		}
		return c.store.DeleteCommunity(ctx, p.CommunityID)

	case events.EventCommunityMemberJoined:
		var p struct {
			CommunityID string `json:"community_id"`
		}
		if err := unmarshalPayload(envelope.Payload, &p); err != nil {
			return err
		}
		return c.store.AddToEngagementScore(ctx, search.IndexCommunities, p.CommunityID, 1)

	case events.EventCommunityMemberLeft, events.EventCommunityMemberBanned:
		var p struct {
			CommunityID string `json:"community_id"`
		}
		if err := unmarshalPayload(envelope.Payload, &p); err != nil {
			return err
		}
		return c.store.AddToEngagementScore(ctx, search.IndexCommunities, p.CommunityID, -1)

	// --- Channels ---
	case events.EventChannelCreated:
		var p channelCreatedPayload
		if err := unmarshalPayload(envelope.Payload, &p); err != nil {
			return err
		}
		return c.store.IndexChannel(ctx, search.ChannelDoc{
			ChannelID:   p.ChannelID,
			OwnerID:     p.OwnerID,
			Name:        p.Name,
			ChannelType: p.ChannelType,
			CreatedAt:   p.CreatedAt,
		})

	case events.EventChannelUpdated:
		var p channelUpdatedPayload
		if err := unmarshalPayload(envelope.Payload, &p); err != nil {
			return err
		}
		return c.store.IndexChannel(ctx, search.ChannelDoc{
			ChannelID: p.ChannelID,
			CreatedAt: p.UpdatedAt,
		})

	case events.EventChannelDeleted:
		var p channelDeletedPayload
		if err := unmarshalPayload(envelope.Payload, &p); err != nil {
			return err
		}
		return c.store.DeleteChannel(ctx, p.ChannelID)

	case events.EventChannelSubscribed:
		var p struct {
			ChannelID string `json:"channel_id"`
		}
		if err := unmarshalPayload(envelope.Payload, &p); err != nil {
			return err
		}
		return c.store.AddToEngagementScore(ctx, search.IndexChannels, p.ChannelID, 1)

	case events.EventChannelUnsubscribed:
		var p struct {
			ChannelID string `json:"channel_id"`
		}
		if err := unmarshalPayload(envelope.Payload, &p); err != nil {
			return err
		}
		return c.store.AddToEngagementScore(ctx, search.IndexChannels, p.ChannelID, -1)

	// --- Products / Commerce ---
	case events.ProductListed:
		var p events.ProductListedPayload
		if err := unmarshalPayload(envelope.Payload, &p); err != nil {
			return err
		}
		return c.store.IndexProductDoc(ctx, search.ProductDoc{
			ProductID: p.ProductID,
			SellerID:  p.SellerID,
			Title:     p.Title,
			Category:  p.Category,
			Price:     p.Price,
			CreatedAt: p.CreatedAt,
		})

	case events.EventOrderCreated, events.OrderCreated:
		// Bump every line item's purchase_count by 1. The order payload
		// doesn't carry product ids here — best we can do without a
		// follow-up call is bump the listing's order-related counter
		// only when commerce-service starts emitting per-line events.
		// (Tracked separately; engagement still updates from views.)
		return nil

	default:
		// Account control (auth-service 30-day deletion flow): hide /
		// unhide / purge. These never dead-letter — HandleUntilDurable
		// blocks this call, retrying with its own escalating backoff,
		// until it succeeds, the payload is permanently undecodable, or
		// ctx is cancelled. On cancellation it returns false and this
		// returns ctx.Err(), which the outer handleUntilDurable() loop
		// recognises as "shutting down" (via ctx.Err() != nil) and leaves
		// the offset uncommitted for redelivery, rather than routing an
		// unresolved purge/hide to the DLQ.
		if c.lifecycle != nil && purge.Handles(envelope.EventType) {
			if !c.lifecycle.HandleUntilDurable(ctx, envelope.EventType, envelope.Payload) {
				return ctx.Err()
			}
			return nil
		}
		return nil
	}
}

// --- Local payload shapes for community/channel events --------------------
//
// The community-service / channel-service producer packages own the
// canonical structs but search-service is a downstream consumer that
// shouldn't import the producer module just for type info. The payload
// JSON is stable, so we declare the field subset we need locally.

type communityCreatedPayload struct {
	CommunityID   string    `json:"community_id"`
	OwnerID       string    `json:"owner_id"`
	Name          string    `json:"name"`
	CommunityType string    `json:"community_type"`
	CreatedAt     time.Time `json:"created_at"`
}

type communityUpdatedPayload struct {
	CommunityID   string    `json:"community_id"`
	Name          string    `json:"name,omitempty"`
	CommunityType string    `json:"community_type,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type communityDeletedPayload struct {
	CommunityID string `json:"community_id"`
}

type channelCreatedPayload struct {
	ChannelID   string    `json:"channel_id"`
	OwnerID     string    `json:"owner_id"`
	Name        string    `json:"name"`
	ChannelType string    `json:"channel_type"`
	CreatedAt   time.Time `json:"created_at"`
}

type channelUpdatedPayload struct {
	ChannelID string    `json:"channel_id"`
	UpdatedAt time.Time `json:"updated_at"`
}

type channelDeletedPayload struct {
	ChannelID string `json:"channel_id"`
}

func unmarshalPayload(raw json.RawMessage, v interface{}) error {
	b, _ := json.Marshal(raw)
	return json.Unmarshal(b, v)
}

func (c *Consumer) Close() error {
	if c.dlq != nil {
		_ = c.dlq.Close()
	}
	return c.reader.Close()
}

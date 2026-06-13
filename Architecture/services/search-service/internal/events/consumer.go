package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

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
	reader    *kafka.Reader
	store     *search.Store
	dlq       *kafka.Writer // optional; nil = log-only on failure
	dlqTopic  string
	groupID   string
	topic     string
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
	}
}

func (c *Consumer) Start(ctx context.Context) {
	for {
		m, err := c.reader.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				slog.Info("search consumer shutting down")
				return
			}
			slog.Error("kafka consumer error", "error", err)
			time.Sleep(2 * time.Second)
			continue
		}

		if err := c.processMessage(ctx, m); err != nil {
			slog.Error("failed to process message", "topic", m.Topic, "offset", m.Offset, "error", err)
			c.sendToDLQ(ctx, m, err)
		}
	}
}

// sendToDLQ best-effort writes a failed message to the DLQ topic so an
// operator/sweeper can inspect it. We never block on DLQ failure —
// the search index isn't critical-path durable storage.
func (c *Consumer) sendToDLQ(ctx context.Context, m kafka.Message, processErr error) {
	if c.dlq == nil {
		return
	}
	dlqCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
		return
	}
	slog.Warn("search: message routed to DLQ", "dlq_topic", c.dlqTopic, "offset", m.Offset)
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

		return c.store.IndexUser(ctx, search.UserDoc{
			UserID:      p.UserID,
			DisplayName: displayName,
		})

	case events.UserProfileUpdated:
		var p events.UserProfileUpdatedPayload
		if err := unmarshalPayload(envelope.Payload, &p); err != nil {
			return err
		}

		return c.store.IndexUser(ctx, search.UserDoc{
			UserID:        p.UserID,
			Username:      p.Username,
			DisplayName:   p.DisplayName,
			Bio:           p.Bio,
			AvatarMediaID: p.AvatarMediaID,
			IsVerified:    p.IsVerified,
		})

	case events.PostCreated:
		var p events.PostCreatedPayload
		if err := unmarshalPayload(envelope.Payload, &p); err != nil {
			return err
		}

		hashtags := extractHashtags(p.Text)

		if err := c.store.IndexPost(ctx, search.PostDoc{
			PostID:     p.PostID,
			AuthorID:   p.AuthorID,
			Text:       p.Text,
			Hashtags:   hashtags,
			Visibility: p.Visibility,
			CreatedAt:  p.CreatedAt,
		}); err != nil {
			return err
		}
		// Mirror hashtag mentions into the hashtags_v1 index. Each tag's
		// use_count + engagement_score bumps by 1. Failures are logged but
		// not bubbled — a missed hashtag tick is acceptable, a missed
		// post-index is not.
		for _, h := range hashtags {
			if err := c.store.IncrementHashtagUse(ctx, h); err != nil {
				slog.Warn("search: hashtag increment failed", "tag", h, "err", err)
			}
		}
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

	case events.PostDeleted:
		var p events.PostDeletedPayload
		if err := unmarshalPayload(envelope.Payload, &p); err != nil {
			return err
		}
		return c.store.DeletePost(ctx, p.PostID)

	case events.EventUserDeletionRequested:
		var p events.UserDeletionRequestedPayload
		if err := unmarshalPayload(envelope.Payload, &p); err != nil {
			return err
		}
		if err := c.store.DeletePostsByAuthor(ctx, p.UserID); err != nil {
			slog.Error("search: failed to delete posts by author", "user_id", p.UserID, "error", err)
		}
		return c.store.DeleteUser(ctx, p.UserID)

	case events.CrosspostRemoved:
		var p events.CrosspostRemovedPayload
		if err := unmarshalPayload(envelope.Payload, &p); err != nil {
			return err
		}
		// Delete the embed post from the search index
		if err := c.store.DeletePost(ctx, p.TargetPostID); err != nil {
			slog.Error("search: failed to delete crosspost target", "target_post_id", p.TargetPostID, "error", err)
		}
		return nil

	case events.UploadDeleted:
		var p events.UploadDeletedPayload
		if err := unmarshalPayload(envelope.Payload, &p); err != nil {
			return err
		}
		return c.store.DeletePost(ctx, p.PostID)

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


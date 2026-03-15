package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"

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
	reader *kafka.Reader
	store  *search.Store
}

func NewConsumer(brokers []string, groupID string, topic string, store *search.Store) *Consumer {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		GroupID:  groupID,
		Topic:    topic,
		MinBytes: 10e3,
		MaxBytes: 10e6,
	})
	return &Consumer{reader: reader, store: store}
}

func (c *Consumer) Start(ctx context.Context) {
	for {
		m, err := c.reader.ReadMessage(ctx)
		if err != nil {
			slog.Error("kafka consumer error", "error", err)
			break
		}

		if err := c.processMessage(ctx, m); err != nil {
			slog.Error("failed to process message", "topic", m.Topic, "offset", m.Offset, "error", err)
		}
	}
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

		return c.store.IndexPost(ctx, search.PostDoc{
			PostID:     p.PostID,
			AuthorID:   p.AuthorID,
			Text:       p.Text,
			Hashtags:   hashtags,
			Visibility: p.Visibility,
			CreatedAt:  p.CreatedAt,
		})

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

	default:
		return nil
	}
}

func unmarshalPayload(raw json.RawMessage, v interface{}) error {
	b, _ := json.Marshal(raw)
	return json.Unmarshal(b, v)
}

func (c *Consumer) Close() error {
	return c.reader.Close()
}

package events

import (
	"context"
	"encoding/json"
	"log"
	"regexp"
	"strings"

	"github.com/facebook-like/search-service/internal/store/search"
	"github.com/facebook-like/shared/events"
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
			log.Printf("Consumer error: %v\n", err)
			break
		}

		if err := c.processMessage(ctx, m); err != nil {
			log.Printf("Failed to process message: %v\n", err)
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

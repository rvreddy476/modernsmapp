package main

import (
	"context"
	"os"

	"github.com/atpost/chat-message-service/internal/purge"
	pgStore "github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
)

// purgeStoreAdapter adapts *pgStore.ConversationStore to purge.PGStore
// (the purge package keeps its own ConversationRef so it does not import the
// store).
type purgeStoreAdapter struct{ *pgStore.ConversationStore }

func (a purgeStoreAdapter) UserConversations(ctx context.Context, userID uuid.UUID) ([]purge.ConversationRef, error) {
	refs, err := a.ConversationStore.UserConversations(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]purge.ConversationRef, len(refs))
	for i, r := range refs {
		out[i] = purge.ConversationRef{ID: r.ID, CreatedAt: r.CreatedAt}
	}
	return out, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

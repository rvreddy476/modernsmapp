package service

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/redis/go-redis/v9"
)

// unreadKey builds the Redis key for an unread counter based on context type and IDs.
func unreadKey(contextType, contextID, subContextID, userID string) string {
	if subContextID != "" {
		switch contextType {
		case "group":
			return fmt.Sprintf("unread:group:%s:channel:%s:user:%s", contextID, subContextID, userID)
		case "community":
			return fmt.Sprintf("unread:community:%s:space:%s:user:%s", contextID, subContextID, userID)
		}
	}
	return fmt.Sprintf("unread:%s:%s:user:%s", contextType, contextID, userID)
}

// IncrementUnread increments the unread counter for the given context.
// Called by fanout workers when new content is published.
func IncrementUnread(ctx context.Context, rdb *redis.Client, contextType, contextID, subContextID, userID string) error {
	key := unreadKey(contextType, contextID, subContextID, userID)
	if err := rdb.Incr(ctx, key).Err(); err != nil {
		slog.Warn("unread: increment failed", "key", key, "error", err)
		return err
	}
	return nil
}

// ResetUnread resets the unread counter to zero for the given context.
// Called when a user marks content as read.
func ResetUnread(ctx context.Context, rdb *redis.Client, contextType, contextID, subContextID, userID string) error {
	key := unreadKey(contextType, contextID, subContextID, userID)
	if err := rdb.Del(ctx, key).Err(); err != nil {
		slog.Warn("unread: reset failed", "key", key, "error", err)
		return err
	}
	return nil
}

// GetUnread returns the current unread count for the given context.
func GetUnread(ctx context.Context, rdb *redis.Client, contextType, contextID, subContextID, userID string) (int, error) {
	key := unreadKey(contextType, contextID, subContextID, userID)
	val, err := rdb.Get(ctx, key).Result()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	count, err := strconv.Atoi(val)
	if err != nil {
		return 0, fmt.Errorf("unread: invalid counter value %q: %w", val, err)
	}
	return count, nil
}

// BulkUnreadRequest represents the request body for POST /v1/unread/bulk.
type BulkUnreadRequest struct {
	GroupIDs        []string `json:"group_ids,omitempty"`
	ChannelIDs      []string `json:"channel_ids,omitempty"`
	CommunityIDs    []string `json:"community_ids,omitempty"`
	ConversationIDs []string `json:"conversation_ids,omitempty"`
}

// BulkUnreadResponse holds the unread counts organized by context type.
type BulkUnreadResponse struct {
	Groups        map[string]int `json:"groups,omitempty"`
	Channels      map[string]int `json:"channels,omitempty"`
	Communities   map[string]int `json:"communities,omitempty"`
	Conversations map[string]int `json:"conversations,omitempty"`
}

// GetBulkUnread retrieves unread counts for multiple contexts in a single Redis pipeline.
func (s *Service) GetBulkUnread(ctx context.Context, userID string, req *BulkUnreadRequest) (*BulkUnreadResponse, error) {
	pipe := s.rdb.Pipeline()

	type pendingCmd struct {
		contextType string
		contextID   string
		cmd         *redis.StringCmd
	}
	var cmds []pendingCmd

	for _, gid := range req.GroupIDs {
		key := fmt.Sprintf("unread:group:%s:user:%s", gid, userID)
		cmd := pipe.Get(ctx, key)
		cmds = append(cmds, pendingCmd{contextType: "group", contextID: gid, cmd: cmd})
	}
	for _, cid := range req.ChannelIDs {
		key := fmt.Sprintf("unread:channel:%s:user:%s", cid, userID)
		cmd := pipe.Get(ctx, key)
		cmds = append(cmds, pendingCmd{contextType: "channel", contextID: cid, cmd: cmd})
	}
	for _, cid := range req.CommunityIDs {
		key := fmt.Sprintf("unread:community:%s:user:%s", cid, userID)
		cmd := pipe.Get(ctx, key)
		cmds = append(cmds, pendingCmd{contextType: "community", contextID: cid, cmd: cmd})
	}
	for _, cid := range req.ConversationIDs {
		key := fmt.Sprintf("unread:chat:%s:user:%s", cid, userID)
		cmd := pipe.Get(ctx, key)
		cmds = append(cmds, pendingCmd{contextType: "conversation", contextID: cid, cmd: cmd})
	}

	if len(cmds) == 0 {
		return &BulkUnreadResponse{}, nil
	}

	_, err := pipe.Exec(ctx)
	if err != nil && err != redis.Nil {
		// Pipeline exec may return redis.Nil if some keys don't exist; that's fine.
		// Only fail on actual connection errors.
		allNil := true
		for _, c := range cmds {
			if c.cmd.Err() != nil && c.cmd.Err() != redis.Nil {
				allNil = false
				break
			}
		}
		if !allNil {
			slog.Warn("bulk unread: pipeline exec error", "error", err)
			return nil, err
		}
	}

	resp := &BulkUnreadResponse{
		Groups:        make(map[string]int),
		Channels:      make(map[string]int),
		Communities:   make(map[string]int),
		Conversations: make(map[string]int),
	}

	for _, c := range cmds {
		val, cmdErr := c.cmd.Result()
		count := 0
		if cmdErr == nil {
			count, _ = strconv.Atoi(val)
		}

		switch c.contextType {
		case "group":
			resp.Groups[c.contextID] = count
		case "channel":
			resp.Channels[c.contextID] = count
		case "community":
			resp.Communities[c.contextID] = count
		case "conversation":
			resp.Conversations[c.contextID] = count
		}
	}

	return resp, nil
}

// ReadMarkerRequest represents the request body for POST /v1/read-marker.
type ReadMarkerRequest struct {
	ContextType  string `json:"context_type" binding:"required"`
	ContextID    string `json:"context_id" binding:"required"`
	SubContextID string `json:"sub_context_id,omitempty"`
	LastReadID   string `json:"last_read_id" binding:"required"`
}

// SetReadMarker sets a read marker and resets the corresponding unread counter.
func (s *Service) SetReadMarker(ctx context.Context, userID string, req *ReadMarkerRequest) error {
	validTypes := map[string]bool{
		"group": true, "channel": true, "community_space": true, "chat": true,
	}
	if !validTypes[req.ContextType] {
		return fmt.Errorf("invalid context_type: must be group, channel, community_space, or chat")
	}

	// Build the read marker key
	var markerKey string
	switch req.ContextType {
	case "group":
		if req.SubContextID != "" {
			markerKey = fmt.Sprintf("read_marker:group:%s:channel:%s:user:%s", req.ContextID, req.SubContextID, userID)
		} else {
			markerKey = fmt.Sprintf("read_marker:group:%s:user:%s", req.ContextID, userID)
		}
	case "channel":
		markerKey = fmt.Sprintf("read_marker:channel:%s:user:%s", req.ContextID, userID)
	case "community_space":
		if req.SubContextID != "" {
			markerKey = fmt.Sprintf("read_marker:community:%s:space:%s:user:%s", req.ContextID, req.SubContextID, userID)
		} else {
			markerKey = fmt.Sprintf("read_marker:community:%s:user:%s", req.ContextID, userID)
		}
	case "chat":
		markerKey = fmt.Sprintf("read_marker:chat:%s:user:%s", req.ContextID, userID)
	}

	// Set the read marker
	if err := s.rdb.Set(ctx, markerKey, req.LastReadID, 0).Err(); err != nil {
		slog.Error("read marker: set failed", "key", markerKey, "error", err)
		return err
	}

	// Reset the unread counter for the main context
	unreadContextType := req.ContextType
	if req.ContextType == "community_space" {
		unreadContextType = "community"
	}
	if err := ResetUnread(ctx, s.rdb, unreadContextType, req.ContextID, "", userID); err != nil {
		slog.Warn("read marker: reset unread failed", "error", err)
	}

	// Also reset sub-context counter if provided
	if req.SubContextID != "" {
		if err := ResetUnread(ctx, s.rdb, unreadContextType, req.ContextID, req.SubContextID, userID); err != nil {
			slog.Warn("read marker: reset sub-context unread failed", "error", err)
		}
	}

	return nil
}

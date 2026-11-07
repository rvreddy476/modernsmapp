// post_hashtags_mentions.go parses a caption for #hashtags and @mentions:
// extraction, the blocked-hashtag filter, the per-post mention cap and the
// mention rows written after a create. Moved out of post.go unchanged.

package service

import (
	"context"
	"log"
	"strings"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// extractHashtags parses #hashtag patterns from text.
func extractHashtags(text string) []string {
	matches := hashtagRegex.FindAllStringSubmatch(text, -1)
	seen := make(map[string]bool)
	var tags []string
	for _, match := range matches {
		tag := strings.ToLower(match[1])
		if !seen[tag] {
			seen[tag] = true
			tags = append(tags, tag)
		}
	}
	return tags
}

// filterBlockedHashtags removes any tags that are marked is_blocked=true in
// the hashtags table. Fails open: if the DB check errors, all tags are kept.
func (s *Service) filterBlockedHashtags(ctx context.Context, tags []string) []string {
	if len(tags) == 0 {
		return tags
	}
	blocked, err := s.pgStore.GetBlockedHashtags(ctx, tags)
	if err != nil || len(blocked) == 0 {
		return tags
	}
	blockedSet := make(map[string]bool, len(blocked))
	for _, t := range blocked {
		blockedSet[t] = true
	}
	out := tags[:0]
	for _, t := range tags {
		if !blockedSet[t] {
			out = append(out, t)
		}
	}
	return out
}

// extractMentions parses @username patterns from text.
// maxMentionsPerPost caps the number of unique @-mentions extracted
// from a single post. Audit H3: the per-mention resolver fans out one
// goroutine + one HTTP call to user-service per mention. Without a
// cap, a post containing 100 `@x` tokens spawns 100 in-flight
// requests — a DoS amplifier dressed as a feature. Anything beyond
// this cap is silently dropped; the same cap is used by the
// `user.mentioned` event fan-out below.
const maxMentionsPerPost = 10

func extractMentions(text string) []string {
	matches := mentionRegex.FindAllStringSubmatch(text, -1)
	seen := make(map[string]bool)
	var usernames []string
	for _, match := range matches {
		username := strings.TrimRight(match[1], ".")
		if username == "" {
			continue
		}
		if !seen[username] {
			seen[username] = true
			usernames = append(usernames, username)
			if len(usernames) >= maxMentionsPerPost {
				break
			}
		}
	}
	return usernames
}

// DetectAndStoreMentions parses @username patterns from body text and inserts
// them into the post_mentions table. Each unique username is stored with the
// post ID and post type. Resolution from username to user_id happens at
// notification time.
func DetectAndStoreMentions(ctx context.Context, postID uuid.UUID, postType string, body string, store *postgres.Store) {
	// Use the package-level compiled regex (audit H6: was being
	// recompiled per call) and cap at maxMentionsPerPost (audit H3:
	// was unbounded, allowing a post with 100 @-tokens to fire 100
	// INSERTs).
	matches := dbMentionRegex.FindAllStringSubmatch(body, -1)
	seen := make(map[string]bool)
	inserted := 0
	for _, match := range matches {
		if inserted >= maxMentionsPerPost {
			break
		}
		username := match[1]
		if seen[username] {
			continue
		}
		seen[username] = true
		inserted++
		if err := store.InsertMention(ctx, postID, postType, username); err != nil {
			log.Printf("Warning: failed to insert mention for @%s on post %s: %v", username, postID, err)
		}
	}
}

package service

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/post-service/pkg/graphclient"
	"github.com/google/uuid"
)

// Comment and reply @mentions (T-12, 2026-10-14).
//
// A post already turns its caption mentions into user.mentioned events
// (announcePublishedPost). Comments did nothing with them, so "@sam look"
// under a reel never reached Sam. This is the comment twin, with three rules
// the post path does not need to spell out because a post's audience is
// settled when it is published:
//
//   - never the commenter themself;
//   - never across a block, in either direction (the commenter blocked the
//     handle, or the handle blocked the commenter);
//   - never someone who cannot open the post — a followers-only post, a
//     private account, a private post not shared with them. The mention would
//     deep-link them to a 404, and worse, tell them the post exists;
//   - never someone whose who_can_mention does not allow the commenter
//     (2026-10-24, mention_tag_privacy.go): the comment keeps the text, the
//     notification is dropped silently.
//
// Every lookup fails closed: an unresolved handle, a graph outage or an
// unanswered relationship drops the notification, never the comment.

// maxMentionsPerComment is the per-comment cap: the same as a post's, so a
// comment of fifty "@x" tokens fans out to at most this many lookups.
const maxMentionsPerComment = maxMentionsPerPost

// commentMentionTimeout bounds the whole resolve-and-publish pass. It runs
// after the response, so it only ever delays a notification.
const commentMentionTimeout = 10 * time.Second

// commentMentionHandles returns the distinct handles mentioned in body, in
// first-seen order. It is the caption parser (extractMentions, which already
// dedupes exact repeats and stops at maxMentionsPerPost) plus a
// case-insensitive pass, since usernames are: "@Sam @sam" is one lookup.
func commentMentionHandles(body string) []string {
	raw := extractMentions(body)
	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for _, handle := range raw {
		key := strings.ToLower(handle)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, handle)
		if len(out) >= maxMentionsPerComment {
			break
		}
	}
	return out
}

// commentMentionPublisher sends one user.mentioned event for a comment.
type commentMentionPublisher func(ctx context.Context, mentionedID, commenterID, postID, commentID uuid.UUID) error

// commentMentionPublish is the publisher in use: the test seam when set,
// else the Kafka producer, else nil (no producer wired: nothing to do).
func (s *Service) commentMentionPublish() commentMentionPublisher {
	if s.commentMentionPublisher != nil {
		return s.commentMentionPublisher
	}
	if s.producer != nil {
		return s.producer.PublishUserMentionedInComment
	}
	return nil
}

// commentMentionRecipients resolves the handles in body to the users who may
// be told about the mention, in first-mention order. See the file comment
// for the rules.
func (s *Service) commentMentionRecipients(ctx context.Context, post *postgres.Post, commenterID uuid.UUID, body string) []uuid.UUID {
	if post == nil {
		return nil
	}
	handles := commentMentionHandles(body)
	if len(handles) == 0 {
		return nil
	}

	// Resolve concurrently (at most maxMentionsPerComment requests), keeping
	// the order the handles were written in.
	resolved := make([]uuid.UUID, len(handles))
	var wg sync.WaitGroup
	for i, h := range handles {
		wg.Add(1)
		go func(i int, handle string) {
			defer wg.Done()
			raw, err := s.lookupUserByUsername(ctx, handle)
			if err != nil || raw == "" {
				return
			}
			if id, err := uuid.Parse(raw); err == nil {
				resolved[i] = id
			}
		}(i, h)
	}
	wg.Wait()

	// Drop unresolved handles and the commenter, and dedupe by user: two
	// handles can name one account (a renamed handle, a case variant).
	seen := make(map[uuid.UUID]bool, len(resolved))
	candidates := make([]uuid.UUID, 0, len(resolved))
	for _, id := range resolved {
		if id == uuid.Nil || id == commenterID || seen[id] {
			continue
		}
		seen[id] = true
		candidates = append(candidates, id)
	}
	if len(candidates) == 0 {
		return nil
	}

	// Blocks, both directions, in one graph call from the commenter's side.
	// Unresolved (no graph wired, an outage, a target the graph did not
	// answer for) notifies nobody.
	targets := make([]string, len(candidates))
	for i, id := range candidates {
		targets[i] = id.String()
	}
	rels, err := s.storyAudience.Relationships(ctx, commenterID.String(), targets)
	if err != nil {
		log.Printf("Warning: comment mention block check unresolved; notifying nobody: %v", err)
		return nil
	}

	// who_can_mention, one graph call for every candidate (fails closed).
	mayMention := s.graphCan(ctx, commenterID, graphclient.ActionMention, candidates)

	out := make([]uuid.UUID, 0, len(candidates))
	for _, id := range candidates {
		rel := rels[id.String()]
		if rel.Blocked || rel.BlockedBy {
			continue
		}
		if !mayMention[id] {
			continue
		}
		// The post's own audience, judged as the mentioned user: the same
		// single-post gate the detail page and every engagement use.
		mentioned := id
		if !s.viewerMayViewPost(ctx, post, &mentioned) {
			continue
		}
		out = append(out, id)
	}
	return out
}

// announceCommentMentions resolves and publishes a new comment's mentions in
// the background. Called once per comment, never for an idempotent replay.
func (s *Service) announceCommentMentions(post *postgres.Post, commenterID, commentID uuid.UUID, body string) {
	publish := s.commentMentionPublish()
	if publish == nil || post == nil || !mentionRegex.MatchString(body) {
		return
	}
	// Copy what the goroutine reads: the caller may reuse the post.
	snapshot := *post
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), commentMentionTimeout)
		defer cancel()
		s.publishCommentMentions(ctx, &snapshot, commenterID, commentID, body, publish)
	}()
}

// publishCommentMentions is announceCommentMentions without the goroutine.
// It returns how many events were published.
func (s *Service) publishCommentMentions(ctx context.Context, post *postgres.Post, commenterID, commentID uuid.UUID, body string, publish commentMentionPublisher) int {
	sent := 0
	for _, id := range s.commentMentionRecipients(ctx, post, commenterID, body) {
		if err := publish(ctx, id, commenterID, post.ID, commentID); err != nil {
			log.Printf("Warning: failed to publish comment mention for %s on comment %s: %v", id, commentID, err)
			continue
		}
		sent++
	}
	return sent
}

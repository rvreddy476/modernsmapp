package service

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"sync"

	"github.com/atpost/post-service/pkg/graphclient"
	"github.com/google/uuid"
)

// "Who can mention or tag me" (2026-10-24).
//
// identity user-service stores who_can_mention and who_can_tag (everyone |
// following | no_one, default everyone; "following" = the people that user
// follows). graph-service resolves them as the mention / tag actions of the
// internal content gate (POST /v1/internal/graph/can), together with blocks
// in either direction. This file is post-service's one place that applies
// them, on every path that stores a post's mentions or tagged people —
// create (and every draft / scheduled publish, which all go through
// CreatePost), the owner edit, and the user.mentioned fan-out at publish
// time (a scheduled post is published later, after a setting may have
// changed) — and on comment mention notifications.
//
// The rule is a SILENT DROP. A disallowed @mention stays in the text as
// plain text but leaves the post's mention list (posts.mention_usernames,
// post_mentions) and is never notified; a disallowed tag leaves
// tagged_user_ids. The create / edit never fails over it: an error would
// tell the author the other person's setting.
//
// Failure shape: DROP. An unreachable graph-service, or a username
// user-service could not resolve because it failed (not "no such user"),
// drops the mention / tag rather than storing one the person may have
// switched off. With no GRAPH_SERVICE_URL (dev rigs) graphCan allows
// everything, the same "no graph, no gate" rule as the other gates.

// mentionLookup resolves one username to a user id: "" with a nil error is
// "no such account"; an error is "could not find out".
func (s *Service) mentionLookup(ctx context.Context, username string) (uuid.UUID, bool, error) {
	raw, err := s.lookupUserByUsername(ctx, username)
	if err != nil {
		return uuid.Nil, false, err
	}
	if raw == "" {
		return uuid.Nil, false, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, false, errors.New("user-service returned a malformed id")
	}
	return id, true, nil
}

// resolvedMention is one username and what it resolved to.
type resolvedMention struct {
	username string
	id       uuid.UUID // uuid.Nil when not found or failed
	found    bool
	err      error
}

// resolveMentions resolves every username concurrently (the list is bounded
// by maxMergedMentions), keeping the input order.
func (s *Service) resolveMentions(ctx context.Context, usernames []string) []resolvedMention {
	out := make([]resolvedMention, len(usernames))
	var wg sync.WaitGroup
	for i, u := range usernames {
		wg.Add(1)
		go func(i int, u string) {
			defer wg.Done()
			id, found, err := s.mentionLookup(ctx, u)
			out[i] = resolvedMention{username: u, id: id, found: found, err: err}
		}(i, u)
	}
	wg.Wait()
	return out
}

// allowedMentions returns the usernames authorID may keep as mentions, in
// order. A username that names no account stays (it is text that links to
// nobody; it is resolved again, and gated, before any notification). A
// lookup failure drops it. The author's own handle always stays.
func (s *Service) allowedMentions(ctx context.Context, authorID uuid.UUID, usernames []string) []string {
	if len(usernames) == 0 {
		return usernames
	}
	resolved := s.resolveMentions(ctx, usernames)
	targets := make([]uuid.UUID, 0, len(resolved))
	for _, r := range resolved {
		if r.found && r.id != authorID {
			targets = append(targets, r.id)
		}
	}
	allowed, answered := s.graphCanResolved(ctx, authorID, graphclient.ActionMention, targets)
	out := make([]string, 0, len(usernames))
	for _, r := range resolved {
		switch {
		case r.err != nil:
			log.Printf("Warning: mention @%s unresolved; dropped: %v", r.username, r.err)
		case !r.found:
			// Kept as text that links to nobody and is never notified.
			// Logged because it is the commonest "my mention did nothing":
			// a handle that is not the account's username.
			slog.Info("post mention names no account; kept as text, nobody notified",
				"author_id", authorID, "username", r.username)
			out = append(out, r.username)
		case r.id == authorID || allowed[r.id]:
			out = append(out, r.username)
		default:
			slog.Info("post mention dropped by who_can_mention",
				"author_id", authorID, "target_id", r.id, "graph_answered", answered)
		}
	}
	return out
}

// allowedTags returns the tagged people authorID may keep, in order.
func (s *Service) allowedTags(ctx context.Context, authorID uuid.UUID, tagged []uuid.UUID) []uuid.UUID {
	if len(tagged) == 0 {
		return tagged
	}
	allowed := s.graphCan(ctx, authorID, graphclient.ActionTag, tagged)
	out := make([]uuid.UUID, 0, len(tagged))
	for _, id := range tagged {
		if allowed[id] {
			out = append(out, id)
		}
	}
	return out
}

// mentionRecipients resolves a post's mentions to the people who may be
// notified: found, not the author, deduped (two handles can name one
// account), and allowed by their who_can_mention right now.
func (s *Service) mentionRecipients(ctx context.Context, authorID uuid.UUID, usernames []string) []uuid.UUID {
	if len(usernames) == 0 {
		return nil
	}
	resolved := s.resolveMentions(ctx, usernames)
	seen := make(map[uuid.UUID]bool, len(resolved))
	candidates := make([]uuid.UUID, 0, len(resolved))
	for _, r := range resolved {
		switch {
		case r.err != nil:
			slog.Warn("post mention not notified: username lookup failed",
				"author_id", authorID, "username", r.username, "error", r.err)
			continue
		case !r.found:
			slog.Info("post mention not notified: names no account",
				"author_id", authorID, "username", r.username)
			continue
		case r.id == authorID || seen[r.id]:
			continue
		}
		seen[r.id] = true
		candidates = append(candidates, r.id)
	}
	if len(candidates) == 0 {
		return nil
	}
	allowed, answered := s.graphCanResolved(ctx, authorID, graphclient.ActionMention, candidates)
	out := make([]uuid.UUID, 0, len(candidates))
	for _, id := range candidates {
		if allowed[id] {
			out = append(out, id)
			continue
		}
		slog.Info("post mention not notified: who_can_mention denies the author",
			"author_id", authorID, "target_id", id, "graph_answered", answered)
	}
	return out
}

// postMentionPublisher sends one user.mentioned event for a post.
type postMentionPublisher func(ctx context.Context, mentionedID, authorID uuid.UUID, postID string) error

// postMentionPublish is the publisher in use: the test seam when set, else
// the Kafka producer, else nil (nothing to do).
func (s *Service) postMentionPublish() postMentionPublisher {
	if s.postMentionPublisher != nil {
		return s.postMentionPublisher
	}
	if s.producer != nil {
		return s.producer.PublishUserMentioned
	}
	return nil
}

// publishPostMentions notifies the allowed recipients of a published post's
// mentions and returns how many events were sent.
func (s *Service) publishPostMentions(ctx context.Context, postID, authorID uuid.UUID, usernames []string) int {
	publish := s.postMentionPublish()
	if publish == nil || len(usernames) == 0 {
		return 0
	}
	sent := 0
	recipients := s.mentionRecipients(ctx, authorID, usernames)
	for _, id := range recipients {
		if err := publish(ctx, id, authorID, postID.String()); err != nil {
			log.Printf("Warning: failed to publish UserMentioned for %s on post %s: %v", id, postID, err)
			continue
		}
		sent++
	}
	// One line per published post with mentions. names>0 with sent=0 is the
	// line to look for when a mention notification never arrives; the reason
	// for each name is logged just before it.
	slog.Info("post mentions announced", "post_id", postID, "author_id", authorID,
		"names", len(usernames), "recipients", len(recipients), "sent", sent)
	return sent
}

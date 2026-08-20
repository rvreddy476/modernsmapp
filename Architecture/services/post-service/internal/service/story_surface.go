package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Module 4 M4-P0-2 — the authorized story surfaces.
//
// Every story a viewer receives passes through EvaluateStoryVisibility here.
// The store queries carry the moderation/deletion predicates as a first cut so
// an unapproved row never leaves PostgreSQL, and this layer applies the
// relationship half of the policy, which the database cannot know.

// ErrStoryNotVisible is the single resolved denial. Handlers turn it into one
// non-enumerating 404 for missing, deleted, expired, unapproved, blocked, and
// out-of-audience alike.
var ErrStoryNotVisible = errors.New("story not visible")

// storyFacts projects a stored row onto the policy input.
func storyFacts(st *postgres.Story, now int64) StoryFacts {
	if st == nil {
		return StoryFacts{Exists: false}
	}
	return StoryFacts{
		Exists:          true,
		Deleted:         st.DeletedAt != nil,
		Expired:         st.ExpiresAt.Unix() <= now,
		IsHighlight:     st.IsHighlight,
		Visibility:      st.Visibility,
		ModerationState: st.ModerationState,
	}
}

// GetStoryForViewer returns a story only if this viewer may see it.
//
// A viewer is REQUIRED. There is no anonymous story read: every story carries
// an audience, and "public" here still means "public to a signed-in viewer we
// can evaluate blocks for". Allowing an anonymous read would make the block
// rules unenforceable by construction.
func (s *Service) GetStoryForViewer(ctx context.Context, viewerID, storyID uuid.UUID) (*postgres.Story, error) {
	if viewerID == uuid.Nil {
		return nil, ErrStoryNotVisible
	}
	story, err := s.pgStore.GetStory(ctx, storyID)
	if err != nil {
		return nil, err
	}
	// A missing story and a denied story take the same path deliberately.
	if story == nil {
		return nil, ErrStoryNotVisible
	}

	rel := ViewerRelationship{}
	if story.AuthorID != viewerID {
		rels, relErr := s.storyAudience.Relationships(ctx, viewerID.String(), []string{story.AuthorID.String()})
		if relErr != nil {
			// Unresolved: propagate so the handler answers 503, not 404.
			return nil, relErr
		}
		rel = rels[story.AuthorID.String()]
	}

	if d := EvaluateStoryVisibility(viewerID.String(), story.AuthorID.String(),
		storyFacts(story, nowUnix()), rel); d != DenyNone {
		return nil, ErrStoryNotVisible
	}
	return story, nil
}

// GetStoriesFeedForViewer returns the viewer's story feed.
//
// The audience is derived from graph-service. No caller supplies it — the
// removed `followed_ids` parameter let any caller name arbitrary authors.
func (s *Service) GetStoriesFeedForViewer(ctx context.Context, viewerID uuid.UUID) ([]postgres.Story, error) {
	if viewerID == uuid.Nil {
		return nil, ErrStoryNotVisible
	}
	authors, err := s.storyAudience.CandidateAuthors(ctx, viewerID.String())
	if err != nil {
		return nil, err
	}
	rels, err := s.storyAudience.Relationships(ctx, viewerID.String(), authors)
	if err != nil {
		return nil, err
	}

	authorUUIDs := make([]uuid.UUID, 0, len(authors))
	for _, a := range authors {
		id, parseErr := uuid.Parse(a)
		if parseErr != nil {
			// An author id the graph returned that will not parse is
			// unresolved state, not a skippable row.
			return nil, fmt.Errorf("%w: graph returned unparseable author %q", ErrStoryPolicyUnresolved, a)
		}
		authorUUIDs = append(authorUUIDs, id)
	}

	rows, err := s.pgStore.GetStoriesFeed(ctx, authorUUIDs)
	if err != nil {
		return nil, err
	}

	now := nowUnix()
	out := make([]postgres.Story, 0, len(rows))
	for i := range rows {
		st := rows[i]
		if d := EvaluateStoryVisibility(viewerID.String(), st.AuthorID.String(),
			storyFacts(&st, now), rels[st.AuthorID.String()]); d == DenyNone {
			out = append(out, st)
		}
	}
	return out, nil
}

// GetStoriesByAuthorForViewer returns one author's stories for this viewer.
func (s *Service) GetStoriesByAuthorForViewer(ctx context.Context, viewerID, authorID uuid.UUID) ([]postgres.Story, error) {
	if viewerID == uuid.Nil {
		return nil, ErrStoryNotVisible
	}
	rel := ViewerRelationship{}
	if authorID != viewerID {
		rels, err := s.storyAudience.Relationships(ctx, viewerID.String(), []string{authorID.String()})
		if err != nil {
			return nil, err
		}
		rel = rels[authorID.String()]
	}

	rows, err := s.pgStore.GetStoriesByAuthor(ctx, authorID)
	if err != nil {
		return nil, err
	}
	now := nowUnix()
	out := make([]postgres.Story, 0, len(rows))
	for i := range rows {
		st := rows[i]
		if d := EvaluateStoryVisibility(viewerID.String(), authorID.String(),
			storyFacts(&st, now), rel); d == DenyNone {
			out = append(out, st)
		}
	}
	return out, nil
}

// ViewStoryForViewer increments the view counter only for a viewer who may
// actually see the story.
//
// M4-P0-2 acceptance criterion 5: a denied, missing, expired, pending or
// rejected view must change neither Redis nor PostgreSQL. The previous handler
// took no viewer at all and incremented for any UUID, so the counter was
// writable by anyone for any id — including ids that did not exist.
func (s *Service) ViewStoryForViewer(ctx context.Context, viewerID, storyID uuid.UUID) error {
	story, err := s.GetStoryForViewer(ctx, viewerID, storyID)
	if err != nil {
		return err
	}
	// The author's own view does not inflate their count.
	if story.AuthorID == viewerID {
		return nil
	}
	return s.adjustStoryViewCount(ctx, storyID)
}

// OwnerStoryStatus returns the author's own stories in every state, including
// pending and rejected, with the truthful moderation reason.
//
// This is the only surface that exposes a non-approved story, and it is
// author-scoped. It exists so the client can show an honest "in review" state
// instead of the story silently not appearing.
func (s *Service) OwnerStoryStatus(ctx context.Context, ownerID uuid.UUID) ([]postgres.Story, error) {
	if ownerID == uuid.Nil {
		return nil, ErrStoryNotVisible
	}
	return s.pgStore.GetStoriesForOwner(ctx, ownerID)
}

// WithStoryAudience wires the server-side audience resolver. Called from
// main.go once the graph client exists.
func (s *Service) WithStoryAudience(a *StoryAudience) *Service {
	s.storyAudience = a
	return s
}

// nowUnix is a seam so expiry can be driven deterministically in tests.
func nowUnix() int64 { return time.Now().Unix() }

// CreateStoryPending creates a story in the pending state with a durable
// moderation request, in one transaction.
//
// It replaces CreateStory, which inserted an immediately-publishable row and
// then emitted StoryCreated from a best-effort goroutine. That ordering allowed
// two independent failures: a story visible before anyone reviewed it, and a
// story whose moderation request was never recorded at all.
func (s *Service) CreateStoryPending(ctx context.Context, input *CreateStoryInput) (*postgres.Story, error) {
	visibility := input.Visibility
	if visibility == "" {
		// Default to the narrowest audience, not the widest. A missing
		// visibility is an unstated intent, and the safe reading of an
		// unstated audience is the smaller one.
		visibility = StoryVisibilityFollowers
	}
	mediaID := input.MediaID
	story := &postgres.Story{
		ID:             uuid.New(),
		AuthorID:       input.AuthorID,
		MediaID:        &mediaID,
		MediaType:      input.MediaType,
		Caption:        input.Caption,
		Visibility:     visibility,
		ExpiresAt:      time.Now().Add(24 * time.Hour),
		IsHighlight:    input.IsHighlight,
		HighlightGroup: input.HighlightGroup,
		CreatedAt:      time.Now(),
	}
	return s.pgStore.CreateStoryPending(ctx, story, input.IdempotencyKey)
}

type MediaAccessDecision string

const (
	DecisionAllowed  MediaAccessDecision = "allowed"
	DecisionNotReady MediaAccessDecision = "not_ready"
	DecisionDenied   MediaAccessDecision = "denied"
)

// MediaAccessResult conveys the binary allowed verdict along with the granular
// decision status and explicit attribution reason.
type MediaAccessResult struct {
	Allowed  bool                `json:"allowed"`
	Decision MediaAccessDecision `json:"decision"`
	Reason   string              `json:"reason"`
}

// ViewerMayAccessMedia reports whether a viewer may receive the bytes of a
// canonical media asset, based on the content that references it.
//
// Module 4 M4-P0-5. This is the exact media-to-owner-content lookup the
// approval requires: it resolves the asset to the content referencing it and
// applies that content's policy, rather than inventing a media-level rule.
//
// UNREFERENCED MEDIA IS DENIED, NOT ALLOWED.
//
// An asset that no live content references has no audience, so there is nobody
// it is authorized for. That covers freshly uploaded media whose post/story was
// never created, and media whose content was deleted — both of which must stop
// being fetchable. The uploader keeps access so an in-progress compose screen
// can still preview its own upload.
func (s *Service) ViewerMayAccessMedia(ctx context.Context, viewerID, mediaID uuid.UUID) (MediaAccessResult, error) {
	if viewerID == uuid.Nil || mediaID == uuid.Nil {
		return MediaAccessResult{Allowed: false, Decision: DecisionDenied, Reason: "nil_id"}, nil
	}

	facts, err := s.pgStore.GetMediaAccessFacts(ctx, mediaID)
	if err != nil {
		return MediaAccessResult{}, err
	}
	if facts == nil {
		slog.InfoContext(ctx, "media access excluded: asset facts not found",
			"viewer_id", viewerID,
			"media_id", mediaID,
			"reason", "not_found")
		return MediaAccessResult{Allowed: false, Decision: DecisionDenied, Reason: "not_found"}, nil
	}
	// Owner preview is the sole pre-publication exception. A rejected or failed
	// asset does not keep minting fresh delivery URLs after takedown/failure.
	if facts.UploaderID == viewerID {
		if facts.ProcessingStatus == "rejected" || facts.ProcessingStatus == "failed" || facts.ModerationStatus == "rejected" {
			slog.InfoContext(ctx, "media access excluded: uploader asset rejected or failed",
				"viewer_id", viewerID,
				"media_id", mediaID,
				"processing_status", facts.ProcessingStatus,
				"moderation_status", facts.ModerationStatus,
				"reason", "uploader_rejected_or_failed")
			return MediaAccessResult{Allowed: false, Decision: DecisionDenied, Reason: "uploader_rejected_or_failed"}, nil
		}
		if facts.ProcessingStatus != "ready" {
			slog.InfoContext(ctx, "media access permitted: uploader asset not ready",
				"viewer_id", viewerID,
				"media_id", mediaID,
				"processing_status", facts.ProcessingStatus,
				"reason", "uploader_not_ready")
			return MediaAccessResult{Allowed: true, Decision: DecisionNotReady, Reason: "uploader_not_ready"}, nil
		}
		slog.DebugContext(ctx, "media access allowed: uploader preview",
			"viewer_id", viewerID,
			"media_id", mediaID,
			"reason", "uploader_allowed")
		return MediaAccessResult{Allowed: true, Decision: DecisionAllowed, Reason: "uploader_allowed"}, nil
	}
	// No content policy can override the canonical media safety gate.
	if facts.ModerationStatus == "rejected" {
		slog.InfoContext(ctx, "media access excluded: moderation rejected",
			"viewer_id", viewerID,
			"media_id", mediaID,
			"reason", "moderation_rejected")
		return MediaAccessResult{Allowed: false, Decision: DecisionDenied, Reason: "moderation_rejected"}, nil
	}

	story, err := s.pgStore.StoryForMedia(ctx, mediaID)
	if err != nil {
		return MediaAccessResult{}, err
	}
	if story != nil {
		rel := ViewerRelationship{}
		if story.AuthorID != viewerID {
			rels, relErr := s.storyAudience.Relationships(ctx, viewerID.String(), []string{story.AuthorID.String()})
			if relErr != nil {
				return MediaAccessResult{}, relErr
			}
			rel = rels[story.AuthorID.String()]
		}
		d := EvaluateStoryVisibility(viewerID.String(), story.AuthorID.String(),
			storyFacts(story, nowUnix()), rel)
		if d == DenyNone {
			if facts.ProcessingStatus != "ready" {
				slog.InfoContext(ctx, "media access permitted: story visible but asset not ready",
					"viewer_id", viewerID,
					"media_id", mediaID,
					"processing_status", facts.ProcessingStatus,
					"reason", "story_not_ready")
				return MediaAccessResult{Allowed: true, Decision: DecisionNotReady, Reason: "story_not_ready"}, nil
			}
			slog.DebugContext(ctx, "media access allowed: story visible",
				"viewer_id", viewerID,
				"media_id", mediaID,
				"reason", "story_allowed")
			return MediaAccessResult{Allowed: true, Decision: DecisionAllowed, Reason: "story_allowed"}, nil
		}
	}

	postVisible, err := s.viewerMayAccessPostMedia(ctx, viewerID, mediaID)
	if err != nil {
		return MediaAccessResult{}, err
	}
	if postVisible {
		if facts.ProcessingStatus != "ready" {
			slog.InfoContext(ctx, "media access permitted: post visible but asset not ready",
				"viewer_id", viewerID,
				"media_id", mediaID,
				"processing_status", facts.ProcessingStatus,
				"reason", "post_not_ready")
			return MediaAccessResult{Allowed: true, Decision: DecisionNotReady, Reason: "post_not_ready"}, nil
		}
		slog.DebugContext(ctx, "media access allowed: post visible",
			"viewer_id", viewerID,
			"media_id", mediaID,
			"reason", "post_allowed")
		return MediaAccessResult{Allowed: true, Decision: DecisionAllowed, Reason: "post_allowed"}, nil
	}

	slog.InfoContext(ctx, "media access excluded: no visible post or story for viewer",
		"viewer_id", viewerID,
		"media_id", mediaID,
		"reason", "no_visible_post_or_story")
	return MediaAccessResult{Allowed: false, Decision: DecisionDenied, Reason: "no_visible_post_or_story"}, nil
}

// ViewerMayAccessMediaBatch evaluates a feed page with a fixed number of
// PostgreSQL queries and one graph relationship batch. It is policy-equivalent
// to ViewerMayAccessMedia; only the data-loading shape differs.
func (s *Service) ViewerMayAccessMediaBatch(ctx context.Context, viewerID uuid.UUID, mediaIDs []uuid.UUID) (map[uuid.UUID]MediaAccessResult, error) {
	results := make(map[uuid.UUID]MediaAccessResult, len(mediaIDs))
	if viewerID == uuid.Nil || len(mediaIDs) == 0 {
		return results, nil
	}
	factsByMedia, err := s.pgStore.GetMediaAccessFactsBatch(ctx, mediaIDs)
	if err != nil {
		return nil, err
	}
	storiesByMedia, err := s.pgStore.StoriesForMediaBatch(ctx, mediaIDs)
	if err != nil {
		return nil, err
	}
	postIDsByMedia, err := s.pgStore.PostIDsByMediaIDs(ctx, mediaIDs)
	if err != nil {
		return nil, err
	}
	postIDSet := make(map[uuid.UUID]bool)
	var postIDs []uuid.UUID
	for _, ids := range postIDsByMedia {
		for _, id := range ids {
			if !postIDSet[id] {
				postIDSet[id] = true
				postIDs = append(postIDs, id)
			}
		}
	}
	posts, err := s.pgStore.GetPostsByIDs(ctx, postIDs)
	if err != nil {
		return nil, err
	}
	postsByID := make(map[uuid.UUID]*postgres.Post, len(posts))
	authorSet := make(map[string]bool)
	var authorIDs []string
	addAuthor := func(id uuid.UUID) {
		if id == viewerID {
			return
		}
		value := id.String()
		if !authorSet[value] {
			authorSet[value] = true
			authorIDs = append(authorIDs, value)
		}
	}
	for i := range posts {
		postsByID[posts[i].ID] = &posts[i]
		if strings.EqualFold(posts[i].ReviewStatus, "approved") {
			addAuthor(posts[i].AuthorID)
		}
	}
	for _, story := range storiesByMedia {
		if story != nil {
			addAuthor(story.AuthorID)
		}
	}
	rels := map[string]ViewerRelationship{}
	if len(authorIDs) > 0 {
		rels, err = s.storyAudience.Relationships(ctx, viewerID.String(), authorIDs)
		if err != nil {
			return nil, err
		}
	}

	for _, mediaID := range mediaIDs {
		facts, ok := factsByMedia[mediaID]
		if !ok {
			slog.InfoContext(ctx, "media access excluded: asset facts not found",
				"viewer_id", viewerID,
				"media_id", mediaID,
				"reason", "not_found")
			results[mediaID] = MediaAccessResult{
				Allowed:  false,
				Decision: DecisionDenied,
				Reason:   "not_found",
			}
			continue
		}

		if facts.UploaderID == viewerID {
			if facts.ProcessingStatus == "rejected" || facts.ProcessingStatus == "failed" || facts.ModerationStatus == "rejected" {
				slog.InfoContext(ctx, "media access excluded: uploader asset rejected or failed",
					"viewer_id", viewerID,
					"media_id", mediaID,
					"processing_status", facts.ProcessingStatus,
					"moderation_status", facts.ModerationStatus,
					"reason", "uploader_rejected_or_failed")
				results[mediaID] = MediaAccessResult{
					Allowed:  false,
					Decision: DecisionDenied,
					Reason:   "uploader_rejected_or_failed",
				}
				continue
			}
			if facts.ProcessingStatus != "ready" {
				slog.InfoContext(ctx, "media access permitted: uploader asset not ready",
					"viewer_id", viewerID,
					"media_id", mediaID,
					"processing_status", facts.ProcessingStatus,
					"reason", "uploader_not_ready")
				results[mediaID] = MediaAccessResult{
					Allowed:  true,
					Decision: DecisionNotReady,
					Reason:   "uploader_not_ready",
				}
				continue
			}
			slog.DebugContext(ctx, "media access allowed: uploader preview",
				"viewer_id", viewerID,
				"media_id", mediaID,
				"reason", "uploader_allowed")
			results[mediaID] = MediaAccessResult{
				Allowed:  true,
				Decision: DecisionAllowed,
				Reason:   "uploader_allowed",
			}
			continue
		}

		if facts.ModerationStatus == "rejected" {
			slog.InfoContext(ctx, "media access excluded: moderation rejected",
				"viewer_id", viewerID,
				"media_id", mediaID,
				"reason", "moderation_rejected")
			results[mediaID] = MediaAccessResult{
				Allowed:  false,
				Decision: DecisionDenied,
				Reason:   "moderation_rejected",
			}
			continue
		}

		if story := storiesByMedia[mediaID]; story != nil {
			decision := EvaluateStoryVisibility(viewerID.String(), story.AuthorID.String(),
				storyFacts(story, nowUnix()), rels[story.AuthorID.String()])
			if decision == DenyNone {
				if facts.ProcessingStatus != "ready" {
					slog.InfoContext(ctx, "media access permitted: story visible but asset not ready",
						"viewer_id", viewerID,
						"media_id", mediaID,
						"processing_status", facts.ProcessingStatus,
						"reason", "story_not_ready")
					results[mediaID] = MediaAccessResult{
						Allowed:  true,
						Decision: DecisionNotReady,
						Reason:   "story_not_ready",
					}
				} else {
					slog.DebugContext(ctx, "media access allowed: story visible",
						"viewer_id", viewerID,
						"media_id", mediaID,
						"reason", "story_allowed")
					results[mediaID] = MediaAccessResult{
						Allowed:  true,
						Decision: DecisionAllowed,
						Reason:   "story_allowed",
					}
				}
				continue
			}
		}

		postAllowed := false
		for _, postID := range postIDsByMedia[mediaID] {
			post := postsByID[postID]
			if post == nil || !strings.EqualFold(post.ReviewStatus, "approved") {
				continue
			}
			if evaluatePostMediaVisibility(viewerID, post, rels[post.AuthorID.String()]) {
				postAllowed = true
				break
			}
		}

		if postAllowed {
			if facts.ProcessingStatus != "ready" {
				slog.InfoContext(ctx, "media access permitted: post visible but asset not ready",
					"viewer_id", viewerID,
					"media_id", mediaID,
					"processing_status", facts.ProcessingStatus,
					"reason", "post_not_ready")
				results[mediaID] = MediaAccessResult{
					Allowed:  true,
					Decision: DecisionNotReady,
					Reason:   "post_not_ready",
				}
			} else {
				slog.DebugContext(ctx, "media access allowed: post visible",
					"viewer_id", viewerID,
					"media_id", mediaID,
					"reason", "post_allowed")
				results[mediaID] = MediaAccessResult{
					Allowed:  true,
					Decision: DecisionAllowed,
					Reason:   "post_allowed",
				}
			}
			continue
		}

		slog.InfoContext(ctx, "media access excluded: no visible post or story for viewer",
			"viewer_id", viewerID,
			"media_id", mediaID,
			"reason", "no_visible_post_or_story")
		results[mediaID] = MediaAccessResult{
			Allowed:  false,
			Decision: DecisionDenied,
			Reason:   "no_visible_post_or_story",
		}
	}
	return results, nil
}

// viewerMayAccessPostMedia extends protected delivery to the shared canonical
// media used by posts, Reels and PostTube. PostTube stays a separate product
// surface; this merely honors its existing post/media reference and never
// creates or merges a second video record.
func (s *Service) viewerMayAccessPostMedia(ctx context.Context, viewerID, mediaID uuid.UUID) (bool, error) {
	postIDs, err := s.pgStore.PostIDsByMediaID(ctx, mediaID)
	if err != nil {
		return false, err
	}
	posts := make([]*postgres.Post, 0, len(postIDs))
	authors := make([]string, 0, len(postIDs))
	seenAuthor := make(map[string]bool)
	for _, id := range postIDs {
		p, err := s.pgStore.GetPost(ctx, id)
		if err != nil {
			return false, err
		}
		if p == nil || !strings.EqualFold(p.ReviewStatus, "approved") {
			continue
		}
		posts = append(posts, p)
		author := p.AuthorID.String()
		if !seenAuthor[author] {
			seenAuthor[author] = true
			authors = append(authors, author)
		}
	}
	if len(posts) == 0 {
		return false, nil
	}
	rels, err := s.storyAudience.Relationships(ctx, viewerID.String(), authors)
	if err != nil {
		return false, err
	}
	for _, p := range posts {
		rel := rels[p.AuthorID.String()]
		if evaluatePostMediaVisibility(viewerID, p, rel) {
			return true, nil
		}
	}
	return false, nil
}

func evaluatePostMediaVisibility(viewerID uuid.UUID, p *postgres.Post, rel ViewerRelationship) bool {
	if p == nil || p.AuthorID == viewerID || !strings.EqualFold(p.ReviewStatus, "approved") {
		return p != nil && p.AuthorID == viewerID
	}
	if rel.Blocked || rel.BlockedBy || rel.Muted {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(p.Visibility)) {
	case "", "public", "unlisted":
		return true
	case "followers":
		return rel.Follows
	case "circle", "trusted", "close_friends":
		return rel.ViewerIsCloseFriendOfTarget
	default: // private, staged, and future/unknown values fail closed
		return false
	}
}

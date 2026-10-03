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
	// A story is a publication: author standing first (publish_standing.go).
	if err := s.requirePublishStanding(ctx, input.AuthorID); err != nil {
		return nil, err
	}
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

// EvaluateMediaAccessFacts is the pure canonical media safety gate.
// For the uploader: allows preview unless processing or moderation is rejected/failed.
// For non-uploaders: requires moderation status to be explicitly "passed" or "approved".
// Returns (terminal, res).
// If terminal is true, the result is definitive (owner allowed/denied, or non-owner moderation denied).
// If terminal is false, evaluation proceeds to story/post content visibility checks.
func EvaluateMediaAccessFacts(viewerID, uploaderID uuid.UUID, processingStatus, moderationStatus string) (bool, MediaAccessResult) {
	if uploaderID == viewerID {
		if processingStatus == "rejected" || processingStatus == "failed" || moderationStatus == "rejected" {
			return true, MediaAccessResult{Allowed: false, Decision: DecisionDenied, Reason: "uploader_rejected_or_failed"}
		}
		if processingStatus != "ready" {
			return true, MediaAccessResult{Allowed: true, Decision: DecisionNotReady, Reason: "uploader_not_ready"}
		}
		return true, MediaAccessResult{Allowed: true, Decision: DecisionAllowed, Reason: "uploader_allowed"}
	}
	// No content policy can override the canonical media safety gate. Non-owner
	// delivery requires an explicitly approved/passed canonical media verdict.
	// pending, manual_review, empty/unknown, scanner failure, and rejected must never produce protected URLs.
	if moderationStatus != "passed" && moderationStatus != "approved" {
		return true, MediaAccessResult{Allowed: false, Decision: DecisionDenied, Reason: "moderation_not_approved"}
	}
	return false, MediaAccessResult{}
}

// / ViewerMayAccessMedia reports whether a viewer may receive the bytes of a
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
//
// viewerID == uuid.Nil is the SIGNED-OUT viewer (2026-09-29, founder decision
// 2): stories have no anonymous reading, a channel avatar is public, and a
// post plays only under anonymousMayAccessPost (media_access.go).
func (s *Service) ViewerMayAccessMedia(ctx context.Context, viewerID, mediaID uuid.UUID) (MediaAccessResult, error) {
	return s.ViewerMayAccessMediaFor(ctx, viewerID, mediaID, "")
}

// ViewerMayAccessMediaFor is ViewerMayAccessMedia for a read that says what
// it is for. purpose MediaAccessPurposePoster is a thumbnail still of the
// asset: the one thing a members-only post shows a signed-in viewer who is
// not a member (the join card's poster). Every other purpose is playback and
// answers to the membership clause; a signed-out viewer is refused a
// members-only post's media whatever the purpose.
//
// A members-only asset whose membership lookup did not answer is refused
// with ErrStoryPolicyUnresolved (503 on the wire): retryable, never cached.
func (s *Service) ViewerMayAccessMediaFor(ctx context.Context, viewerID, mediaID uuid.UUID, purpose string) (MediaAccessResult, error) {
	if mediaID == uuid.Nil {
		return MediaAccessResult{Allowed: false, Decision: DecisionDenied, Reason: "nil_id"}, nil
	}
	if s.mediaAccess == nil {
		return MediaAccessResult{}, errMediaAccessUnwired()
	}

	facts, err := s.mediaAccess.GetMediaAccessFacts(ctx, mediaID)
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

	if terminal, res := EvaluateMediaAccessFacts(viewerID, facts.UploaderID, facts.ProcessingStatus, facts.ModerationStatus); terminal {
		if !res.Allowed {
			slog.InfoContext(ctx, "media access excluded: canonical gate",
				"viewer_id", viewerID,
				"media_id", mediaID,
				"processing_status", facts.ProcessingStatus,
				"moderation_status", facts.ModerationStatus,
				"reason", res.Reason)
		} else {
			slog.DebugContext(ctx, "media access allowed: canonical gate",
				"viewer_id", viewerID,
				"media_id", mediaID,
				"reason", res.Reason)
		}
		return res, nil
	}

	// Tube: a channel avatar is public to every viewer once the canonical
	// gate above (moderation) has let a non-uploader through.
	if owners, err := s.mediaAccess.ChannelAvatarOwners(ctx, []uuid.UUID{mediaID}); err != nil {
		return MediaAccessResult{}, err
	} else if _, isAvatar := owners[mediaID]; isAvatar {
		return channelAvatarAccess(facts.ProcessingStatus), nil
	}

	if viewerID != uuid.Nil {
		// Stories carry an audience that needs a viewer to evaluate blocks
		// for (GetStoryForViewer); a signed-out viewer never reaches this.
		story, err := s.mediaAccess.StoryForMedia(ctx, mediaID)
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
				return storyMediaVerdict(ctx, viewerID, mediaID, facts.ProcessingStatus), nil
			}
		}
	}

	answer, err := s.viewerMayAccessPostMedia(ctx, viewerID, mediaID, purpose == MediaAccessPurposePoster)
	if err != nil {
		return MediaAccessResult{}, err
	}
	switch answer {
	case postMediaPlays:
		return postMediaVerdict(ctx, viewerID, mediaID, facts.ProcessingStatus), nil
	case postMediaMembersOnly:
		return membersOnlyDenied(ctx, viewerID, mediaID), nil
	case postMediaMembershipUnresolved:
		slog.WarnContext(ctx, "media access unresolved: membership lookup did not answer",
			"viewer_id", viewerID,
			"media_id", mediaID,
			"reason", "membership_unresolved")
		return MediaAccessResult{}, fmt.Errorf("%w: membership lookup did not answer", ErrStoryPolicyUnresolved)
	}
	return noVisibleContent(ctx, viewerID, mediaID), nil
}

// ViewerMayAccessMediaBatch evaluates a feed page with a fixed number of
// database queries rather than one per card. The semantics are identical
// to ViewerMayAccessMedia; only the data-loading shape differs.
func (s *Service) ViewerMayAccessMediaBatch(ctx context.Context, viewerID uuid.UUID, mediaIDs []uuid.UUID) (map[uuid.UUID]MediaAccessResult, error) {
	results := make(map[uuid.UUID]MediaAccessResult, len(mediaIDs))
	if len(mediaIDs) == 0 {
		return results, nil
	}
	if s.mediaAccess == nil {
		return nil, errMediaAccessUnwired()
	}
	anonymous := viewerID == uuid.Nil

	factsByMedia, err := s.mediaAccess.GetMediaAccessFactsBatch(ctx, mediaIDs)
	if err != nil {
		return nil, err
	}
	postIDsByMedia, err := s.mediaAccess.PostIDsByMediaIDs(ctx, mediaIDs)
	if err != nil {
		return nil, err
	}
	avatarOwners, err := s.mediaAccess.ChannelAvatarOwners(ctx, mediaIDs)
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
	posts, err := s.mediaAccess.GetPostsByIDs(ctx, postIDs)
	if err != nil {
		return nil, err
	}
	postsByID := make(map[uuid.UUID]*postgres.Post, len(posts))
	candidates := make([]*postgres.Post, 0, len(posts))
	for i := range posts {
		postsByID[posts[i].ID] = &posts[i]
		if strings.EqualFold(posts[i].EffectiveReviewStatus(), "approved") {
			candidates = append(candidates, &posts[i])
		}
	}
	// One resolution of the account gate, relationships, shares and age for
	// the whole page.
	decision, err := s.postMediaDecisions(ctx, viewerID, candidates)
	if err != nil {
		return nil, err
	}

	// Stories: signed-in only, with their own relationship lookup (a story's
	// author is rarely one of the page's post authors).
	storiesByMedia := map[uuid.UUID]*postgres.Story{}
	storyRels := map[string]ViewerRelationship{}
	if !anonymous {
		storiesByMedia, err = s.mediaAccess.StoriesForMediaBatch(ctx, mediaIDs)
		if err != nil {
			return nil, err
		}
		var storyAuthors []string
		seenAuthor := map[string]bool{}
		for _, story := range storiesByMedia {
			if story == nil || story.AuthorID == viewerID {
				continue
			}
			if id := story.AuthorID.String(); !seenAuthor[id] {
				seenAuthor[id] = true
				storyAuthors = append(storyAuthors, id)
			}
		}
		if len(storyAuthors) > 0 {
			storyRels, err = s.storyAudience.Relationships(ctx, viewerID.String(), storyAuthors)
			if err != nil {
				return nil, err
			}
		}
	}

	for _, mediaID := range mediaIDs {
		facts, found := factsByMedia[mediaID]
		if !found {
			slog.InfoContext(ctx, "media access excluded: asset facts not found in batch",
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

		if terminal, res := EvaluateMediaAccessFacts(viewerID, facts.UploaderID, facts.ProcessingStatus, facts.ModerationStatus); terminal {
			if !res.Allowed {
				slog.InfoContext(ctx, "media access excluded: canonical gate in batch",
					"viewer_id", viewerID,
					"media_id", mediaID,
					"processing_status", facts.ProcessingStatus,
					"moderation_status", facts.ModerationStatus,
					"reason", res.Reason)
			} else {
				slog.DebugContext(ctx, "media access allowed: canonical gate in batch",
					"viewer_id", viewerID,
					"media_id", mediaID,
					"reason", res.Reason)
			}
			results[mediaID] = res
			continue
		}

		// Tube: a channel avatar is public (see ViewerMayAccessMedia).
		if _, isAvatar := avatarOwners[mediaID]; isAvatar {
			results[mediaID] = channelAvatarAccess(facts.ProcessingStatus)
			continue
		}

		if story := storiesByMedia[mediaID]; story != nil {
			decision := EvaluateStoryVisibility(viewerID.String(), story.AuthorID.String(),
				storyFacts(story, nowUnix()), storyRels[story.AuthorID.String()])
			if decision == DenyNone {
				results[mediaID] = storyMediaVerdict(ctx, viewerID, mediaID, facts.ProcessingStatus)
				continue
			}
		}

		// A page has no variant to name, so nothing here is a poster read;
		// a cover is still recognised as a cover (postCoverOnly).
		carrying := make([]*postgres.Post, 0, len(postIDsByMedia[mediaID]))
		for _, postID := range postIDsByMedia[mediaID] {
			carrying = append(carrying, postsByID[postID])
		}
		switch decision.judgePostMedia(carrying, mediaID, false) {
		case postMediaPlays:
			results[mediaID] = postMediaVerdict(ctx, viewerID, mediaID, facts.ProcessingStatus)
		case postMediaMembersOnly:
			results[mediaID] = membersOnlyDenied(ctx, viewerID, mediaID)
		case postMediaMembershipUnresolved:
			results[mediaID] = membershipUnresolvedDenied(ctx, viewerID, mediaID)
		default:
			results[mediaID] = noVisibleContent(ctx, viewerID, mediaID)
		}
	}
	return results, nil
}

// channelAvatarAccess is the verdict for a media asset that is some Tube
// channel's avatar: allowed for every viewer, "not ready" while the image
// pipeline is still running. Moderation was already checked by the
// canonical gate before this is consulted.
func channelAvatarAccess(processingStatus string) MediaAccessResult {
	if processingStatus != "ready" {
		return MediaAccessResult{Allowed: true, Decision: DecisionNotReady, Reason: "channel_avatar_not_ready"}
	}
	return MediaAccessResult{Allowed: true, Decision: DecisionAllowed, Reason: "channel_avatar"}
}

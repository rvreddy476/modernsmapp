// notes_read.go holds the Echo reads and owner commands: one note, its
// replies, its conversation, a profile tab, the caller's eligibility, the
// 30-minute edit and the delete (docs/designs/echo-contract.md).
//
// Every read goes through hydrateNotes, which hands the note ids to the
// batch read feed-service hydrates with (GetPostsByIDs): the same private
// account, block, deactivated-author, review, media and age gates as any
// post, so a note is never readable where its post would not be.
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// noteProfile is one identity-profile batch entry as Echo reads it.
type noteProfile struct {
	Username          string     `json:"username"`
	DisplayName       string     `json:"display_name"`
	AvatarMediaID     *uuid.UUID `json:"avatar_media_id,omitempty"`
	IsVerified        bool       `json:"is_verified"`
	VerificationLevel string     `json:"verification_level"`
	BadgeFlags        int        `json:"badge_flags"`
}

// fetchNoteProfiles is fetchCommentProfiles with the verification fields.
func (s *Service) fetchNoteProfiles(ctx context.Context, viewerID *uuid.UUID, ids []string) (map[uuid.UUID]noteProfile, error) {
	ctx, cancel := context.WithTimeout(ctx, commentAuthorHydrationTimeout)
	defer cancel()
	body, err := json.Marshal(map[string]any{"user_ids": ids})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(s.profileServiceURL, "/")+"/v1/profiles/batch", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if viewerID != nil {
		req.Header.Set("X-User-Id", viewerID.String())
	}
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("identity-profile returned %d", resp.StatusCode)
	}
	out := make(map[uuid.UUID]noteProfile)
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode profile batch: %w", err)
	}
	return out, nil
}

// hydrateNotes turns note rows into the Note JSON for viewerID, in order,
// dropping every note this viewer may not see. withQuotes fills
// quoted_note one level deep (the quoted note's own quote stays null).
func (s *Service) hydrateNotes(ctx context.Context, notes []*postgres.Note, viewerID *uuid.UUID, withQuotes bool) ([]*NoteView, error) {
	out := make([]*NoteView, 0, len(notes))
	if len(notes) == 0 {
		return out, nil
	}
	ids := make([]uuid.UUID, 0, len(notes))
	for _, n := range notes {
		ids = append(ids, n.Post.ID)
	}
	details, err := s.GetPostsByIDs(ctx, ids, viewerID)
	if err != nil {
		return nil, err
	}

	authorSet := map[uuid.UUID]bool{}
	var authorIDs []uuid.UUID
	follows := map[uuid.UUID]bool{}
	for _, n := range notes {
		d := details[n.Post.ID]
		if d == nil || d.Post == nil || d.Post.ContentType != NoteContentType {
			continue
		}
		v := buildNoteView(d, n)
		if viewerID != nil {
			v.ViewerCanReply = s.viewerCanReplyCached(ctx, n, *viewerID, follows)
		}
		out = append(out, v)
		if !authorSet[n.Post.AuthorID] {
			authorSet[n.Post.AuthorID] = true
			authorIDs = append(authorIDs, n.Post.AuthorID)
		}
	}
	if authors := s.noteAuthors(ctx, viewerID, authorIDs); authors != nil {
		for _, v := range out {
			v.Author = authors[v.Post.AuthorID]
		}
	}

	if withQuotes {
		var quoteIDs []uuid.UUID
		for _, v := range out {
			if v.QuoteOfID != nil {
				quoteIDs = append(quoteIDs, *v.QuoteOfID)
			}
		}
		if len(quoteIDs) > 0 {
			quoted, err := s.loadNoteViews(ctx, quoteIDs, viewerID, false)
			if err != nil {
				return nil, err
			}
			for _, v := range out {
				if v.QuoteOfID != nil {
					// A deleted or hidden target reads as null; quote_of_id stays.
					v.QuotedNote = quoted[*v.QuoteOfID]
				}
			}
		}
	}
	return out, nil
}

// viewerCanReplyCached is replyPolicyAllows with the "author follows the
// viewer" answer memoized per author for one page.
func (s *Service) viewerCanReplyCached(ctx context.Context, n *postgres.Note, viewer uuid.UUID, follows map[uuid.UUID]bool) bool {
	if n.ReplyPolicy == ReplyPolicyFollowing && viewer != n.Post.AuthorID {
		if v, ok := follows[n.Post.AuthorID]; ok {
			return v
		}
		v := s.replyPolicyAllows(ctx, n, viewer)
		follows[n.Post.AuthorID] = v
		return v
	}
	return s.replyPolicyAllows(ctx, n, viewer)
}

// loadNoteViews reads and hydrates notes by id; absent = not visible.
func (s *Service) loadNoteViews(ctx context.Context, ids []uuid.UUID, viewerID *uuid.UUID, withQuotes bool) (map[uuid.UUID]*NoteView, error) {
	store, err := s.notesStore()
	if err != nil {
		return nil, err
	}
	rows, err := store.GetNotes(ctx, ids)
	if err != nil {
		return nil, err
	}
	notes := make([]*postgres.Note, 0, len(rows))
	for _, id := range ids {
		if n := rows[id]; n != nil {
			notes = append(notes, n)
		}
	}
	views, err := s.hydrateNotes(ctx, notes, viewerID, withQuotes)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]*NoteView, len(views))
	for _, v := range views {
		out[v.Post.ID] = v
	}
	return out, nil
}

// GetNote is GET /v1/notes/:id.
func (s *Service) GetNote(ctx context.Context, id uuid.UUID, viewerID *uuid.UUID) (*NoteView, error) {
	views, err := s.loadNoteViews(ctx, []uuid.UUID{id}, viewerID, true)
	if err != nil {
		return nil, err
	}
	v := views[id]
	if v == nil {
		return nil, ErrNoteNotFound
	}
	return v, nil
}

func clampNoteLimit(limit int) int {
	if limit <= 0 {
		return NoteListDefault
	}
	if limit > NoteListMax {
		return NoteListMax
	}
	return limit
}

// ListNoteReplies is GET /v1/notes/:id/replies: the direct replies of a
// note this viewer may see, oldest first.
func (s *Service) ListNoteReplies(ctx context.Context, id uuid.UUID, viewerID *uuid.UUID, cursor string, limit int) (*NotePage, error) {
	if _, err := s.GetNote(ctx, id, viewerID); err != nil {
		return nil, err
	}
	store, err := s.notesStore()
	if err != nil {
		return nil, err
	}
	rows, next, err := store.ListNotes(ctx, postgres.NoteListQuery{ParentID: &id, Limit: clampNoteLimit(limit), Cursor: cursor})
	if err != nil {
		return nil, err
	}
	return s.notePage(ctx, rows, next, viewerID)
}

func (s *Service) notePage(ctx context.Context, rows []postgres.Note, next string, viewerID *uuid.UUID) (*NotePage, error) {
	ptrs := make([]*postgres.Note, len(rows))
	for i := range rows {
		ptrs[i] = &rows[i]
	}
	items, err := s.hydrateNotes(ctx, ptrs, viewerID, true)
	if err != nil {
		return nil, err
	}
	return &NotePage{Items: items, NextCursor: next}, nil
}

// NoteConversation is GET /v1/notes/:id/conversation: the visible notes
// from the top of the conversation down to the parent of id.
func (s *Service) NoteConversation(ctx context.Context, id uuid.UUID, viewerID *uuid.UUID) ([]*NoteView, error) {
	store, err := s.notesStore()
	if err != nil {
		return nil, err
	}
	if _, err := s.GetNote(ctx, id, viewerID); err != nil {
		return nil, err
	}
	rows, err := store.GetNotes(ctx, []uuid.UUID{id})
	if err != nil {
		return nil, err
	}
	cur := rows[id]
	var chain []*postgres.Note
	seen := map[uuid.UUID]bool{id: true}
	for cur != nil && cur.ReplyToID != nil && len(chain) < noteConversationCap {
		parentID := *cur.ReplyToID
		if seen[parentID] {
			break
		}
		seen[parentID] = true
		parents, err := store.GetNotes(ctx, []uuid.UUID{parentID})
		if err != nil {
			return nil, err
		}
		cur = parents[parentID]
		if cur != nil {
			chain = append(chain, cur)
		}
	}
	// Top first.
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return s.hydrateNotes(ctx, chain, viewerID, true)
}

// ListUserNotes is GET /v1/notes/users/:userId?tab=notes|replies|reposts.
// A viewer who may not see the account's posts gets an empty page.
func (s *Service) ListUserNotes(ctx context.Context, userID uuid.UUID, viewerID *uuid.UUID, tab, cursor string, limit int) (*NotePage, error) {
	t := postgres.NoteListTab(tab)
	switch t {
	case "":
		t = postgres.NoteTabNotes
	case postgres.NoteTabNotes, postgres.NoteTabReplies, postgres.NoteTabReposts:
	default:
		return nil, ErrInvalidNoteTab
	}
	isOwner := viewerID != nil && *viewerID == userID
	if !isOwner && !s.canViewAuthor(ctx, viewerID, userID) {
		return &NotePage{Items: []*NoteView{}}, nil
	}
	store, err := s.notesStore()
	if err != nil {
		return nil, err
	}
	rows, next, err := store.ListNotes(ctx, postgres.NoteListQuery{
		AuthorID: &userID, Tab: t, IncludeOwn: isOwner && t != postgres.NoteTabReposts,
		Limit: clampNoteLimit(limit), Cursor: cursor,
	})
	if err != nil {
		return nil, err
	}
	return s.notePage(ctx, rows, next, viewerID)
}

// NoteEligibility answers GET /v1/notes/me/eligibility. A source that
// cannot answer is an error (503), never a guess: standing
// (ErrStandingUnknown), premium (ErrPremiumCheckUnavailable) or the
// account's age (ErrAccountCheckUnavailable).
func (s *Service) NoteEligibility(ctx context.Context, userID uuid.UUID) (*NoteEligibility, error) {
	if err := s.requirePublishStanding(ctx, userID); err != nil {
		if errors.Is(err, ErrAuthorSuspended) {
			return &NoteEligibility{Reason: EligibilitySuspended}, nil
		}
		return nil, err
	}
	out := &NoteEligibility{CanPost: true, CanReply: true}
	switch err := s.requirePremium(ctx, userID); {
	case errors.Is(err, ErrPremiumRequired):
		out.CanPost = false
	case err != nil:
		return nil, err
	}
	switch err := s.requireAccountAge(ctx, userID); {
	case errors.Is(err, ErrAccountTooNew):
		out.CanReply = false
	case err != nil:
		return nil, err
	}
	switch {
	case !out.CanPost:
		out.Reason = EligibilityPremiumRequired
	case !out.CanReply:
		out.Reason = EligibilityAccountTooNew
	}
	return out, nil
}

// NotePatch is PATCH /v1/notes/:id.
type NotePatch struct {
	Text        *string
	ReplyPolicy *string
}

// PatchNote edits the caller's note: text within NoteEditWindow of
// creation (stamping edited_at), reply_policy at any time.
func (s *Service) PatchNote(ctx context.Context, id, callerID uuid.UUID, in NotePatch) (*NoteView, error) {
	if in.Text == nil && in.ReplyPolicy == nil {
		return nil, fmt.Errorf("%w: nothing to change", ErrInvalidNoteRequest)
	}
	if in.ReplyPolicy != nil && !ValidReplyPolicy(*in.ReplyPolicy) {
		return nil, fmt.Errorf("%w: reply_policy must be everyone, following or mentioned", ErrInvalidNoteRequest)
	}
	store, err := s.notesStore()
	if err != nil {
		return nil, err
	}
	rows, err := store.GetNotes(ctx, []uuid.UUID{id})
	if err != nil {
		return nil, err
	}
	n := rows[id]
	if n == nil {
		return nil, ErrNoteNotFound
	}
	if n.Post.AuthorID != callerID {
		return nil, ErrNotNoteAuthor
	}
	now := s.clock()
	edit := postgres.NoteEdit{ReplyPolicy: in.ReplyPolicy, EditedAt: now}
	if in.Text != nil {
		if !noteEditWindowOpen(n.Post.CreatedAt, now) {
			return nil, ErrEditWindowClosed
		}
		text := strings.TrimSpace(*in.Text)
		if err := ValidateNoteText(text, len(n.Post.Media)); err != nil {
			return nil, err
		}
		edit.Text = &text
		edit.Hashtags = s.noteHashtags(ctx, text)
		edit.Mentions = s.allowedMentions(ctx, callerID, extractMentions(text))
	}
	ok, err := store.UpdateNote(ctx, id, callerID, edit)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNoteNotFound
	}
	s.InvalidatePostBodyCache(ctx, id)
	return s.GetNote(ctx, id, &callerID)
}

// DeleteNote is DELETE /v1/notes/:id: the ordinary soft delete (Recently
// deleted, restorable), by the author only. A reply gives its parent's
// reply_count back.
func (s *Service) DeleteNote(ctx context.Context, id, callerID uuid.UUID) error {
	store, err := s.notesStore()
	if err != nil {
		return err
	}
	rows, err := store.GetNotes(ctx, []uuid.UUID{id})
	if err != nil {
		return err
	}
	n := rows[id]
	if n == nil {
		return ErrNoteNotFound
	}
	if n.Post.AuthorID != callerID {
		return ErrNotNoteAuthor
	}
	if err := s.deleteOne(ctx, id, callerID); err != nil {
		if errors.Is(err, ErrPostNotFound) {
			return ErrNoteNotFound
		}
		if errors.Is(err, ErrPostForbidden) {
			return ErrNotNoteAuthor
		}
		return err
	}
	if n.ReplyToID != nil {
		if err := store.DecrementNoteReplyCount(ctx, *n.ReplyToID); err != nil {
			slog.Warn("note delete: parent reply_count not decremented", "note_id", id, "parent_id", *n.ReplyToID, "err", err)
		}
	}
	return nil
}

// noteHashtags is a note's hashtags: parsed, capped at 20 and the blocked
// ones dropped (no store in tests: nothing is blocked).
func (s *Service) noteHashtags(ctx context.Context, text string) []string {
	tags := extractHashtags(text)
	if len(tags) > 20 {
		tags = tags[:20]
	}
	if s.pgStore == nil {
		return tags
	}
	return s.filterBlockedHashtags(ctx, tags)
}

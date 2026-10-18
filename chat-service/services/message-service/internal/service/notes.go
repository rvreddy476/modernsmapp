package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Chat parity §5: Notes — a 24-hour status line ("Note").

const (
	NoteTTL          = 24 * time.Hour
	noteMaxGraphemes = 60
	notesFeedCap     = 50
	// noteCandidateCap bounds DM peers + followings considered per feed.
	noteCandidateCap = 500
)

var (
	// ErrInvalidNote is a note outside 1..60 characters or a bad audience.
	ErrInvalidNote = errors.New("invalid note")
	// ErrNoteRejected is a note the layer-1 text rules refuse.
	ErrNoteRejected = errors.New("note text is not allowed")
)

var noteAudiences = map[string]bool{"everyone": true, "friends": true, "only_me": true}

// NoteAuthor is the person beside a note in the feed.
type NoteAuthor struct {
	UserID      uuid.UUID `json:"user_id"`
	DisplayName string    `json:"display_name"`
	AvatarURL   string    `json:"avatar_url"`
	IsOnline    *bool     `json:"is_online"`
}

// NoteFeedItem is one other person's note.
type NoteFeedItem struct {
	Note postgres.Note `json:"note"`
	User NoteAuthor    `json:"user"`
}

// NotesFeed is GET /v1/chat/notes.
type NotesFeed struct {
	Mine   *postgres.Note `json:"mine"`
	Others []NoteFeedItem `json:"others"`
}

// PutMyNote replaces the caller's note.
func (s *Service) PutMyNote(ctx context.Context, userID uuid.UUID, text, audience string) (*postgres.Note, error) {
	ps, ok := s.parity()
	if !ok {
		return nil, errors.New("notes not supported")
	}
	text = strings.TrimSpace(text)
	if n := graphemeCount(text); n < 1 || n > noteMaxGraphemes {
		return nil, fmt.Errorf("%w: text must be 1..%d characters", ErrInvalidNote, noteMaxGraphemes)
	}
	if audience == "" {
		audience = "everyone"
	}
	if !noteAudiences[audience] {
		return nil, fmt.Errorf("%w: audience must be everyone, friends or only_me", ErrInvalidNote)
	}
	if !noteTextAllowed(text) {
		return nil, ErrNoteRejected
	}
	now := s.clock().UTC().Truncate(time.Millisecond)
	n := postgres.Note{ID: uuid.New(), UserID: userID, Text: text, Audience: audience, CreatedAt: now, ExpiresAt: now.Add(NoteTTL)}
	if err := ps.ReplaceNote(ctx, n); err != nil {
		return nil, err
	}
	return &n, nil
}

// DeleteMyNote removes the caller's note (idempotent).
func (s *Service) DeleteMyNote(ctx context.Context, userID uuid.UUID) error {
	ps, ok := s.parity()
	if !ok {
		return errors.New("notes not supported")
	}
	return ps.DeleteNotes(ctx, userID)
}

// noteVisible applies the audience to one viewer.
func noteVisible(n postgres.Note, rel graphRelationship, known bool) bool {
	if !known || rel.blocked() {
		return false
	}
	switch n.Audience {
	case "everyone":
		return true
	case "friends":
		return rel.friends()
	default:
		return false
	}
}

// NotesFeed returns the caller's note and the visible notes of the people
// they have a DM with or follow, newest first, at most 50. Visibility needs
// the graph (blocks, audience); if graph cannot answer, others is empty —
// a note is never shown on a guess.
func (s *Service) NotesFeed(ctx context.Context, userID uuid.UUID) (*NotesFeed, error) {
	ps, ok := s.parity()
	if !ok {
		return &NotesFeed{Others: []NoteFeedItem{}}, nil
	}
	now := s.clock()
	feed := &NotesFeed{Others: []NoteFeedItem{}}
	mine, err := ps.ListLiveNotes(ctx, []uuid.UUID{userID}, now, 1)
	if err != nil {
		return nil, err
	}
	if len(mine) == 1 {
		feed.Mine = &mine[0]
	}

	peers, err := ps.DirectPeerIDs(ctx, userID, noteCandidateCap)
	if err != nil {
		return nil, err
	}
	candidates := map[uuid.UUID]bool{}
	for _, id := range peers {
		candidates[id] = true
	}
	if following, err := s.graphFollowingIDs(ctx, userID); err == nil {
		for _, id := range following {
			if len(candidates) >= noteCandidateCap*2 {
				break
			}
			candidates[id] = true
		}
	}
	delete(candidates, userID)
	if len(candidates) == 0 {
		return feed, nil
	}
	ids := make([]uuid.UUID, 0, len(candidates))
	for id := range candidates {
		ids = append(ids, id)
	}
	notes, err := ps.ListLiveNotes(ctx, ids, now, noteCandidateCap)
	if err != nil {
		return nil, err
	}
	var owners []uuid.UUID
	for _, n := range notes {
		if n.Audience != "only_me" {
			owners = append(owners, n.UserID)
		}
	}
	if len(owners) == 0 {
		return feed, nil
	}
	rels, err := s.graphRelationships(ctx, userID, owners)
	if err != nil {
		s.log.Warn("notes feed: relationship lookup failed; showing no notes from others", "err", err)
		return feed, nil
	}
	var visible []postgres.Note
	for _, n := range notes {
		rel, known := rels[n.UserID]
		if noteVisible(n, rel, known) {
			visible = append(visible, n)
			if len(visible) == notesFeedCap {
				break
			}
		}
	}
	if len(visible) == 0 {
		return feed, nil
	}
	authorIDs := make([]uuid.UUID, len(visible))
	for i, n := range visible {
		authorIDs[i] = n.UserID
	}
	authors := s.authorCards(ctx, userID, authorIDs)
	for _, n := range visible {
		feed.Others = append(feed.Others, NoteFeedItem{Note: n, User: authors[n.UserID]})
	}
	return feed, nil
}

// authorCards resolves name, avatar URL and online state for people, as
// the viewer sees them.
func (s *Service) authorCards(ctx context.Context, viewerID uuid.UUID, ids []uuid.UUID) map[uuid.UUID]NoteAuthor {
	out := map[uuid.UUID]NoteAuthor{}
	members := make([]postgres.Member, len(ids))
	for i, id := range ids {
		members[i] = postgres.Member{UserID: id}
	}
	enriched := s.enrichMembers(ctx, members)
	var avatarIDs []string
	for _, m := range enriched {
		if m.AvatarMediaID != nil {
			avatarIDs = append(avatarIDs, m.AvatarMediaID.String())
		}
	}
	var urls map[string]string
	if len(avatarIDs) > 0 && s.mediaServiceURL != "" && s.httpClient != nil {
		urls = s.fetchMediaURLs(ctx, viewerID, avatarIDs)
	}
	online, _ := s.onlineStates(ctx, viewerID, ids)
	for _, m := range enriched {
		a := NoteAuthor{UserID: m.UserID, DisplayName: m.DisplayName, IsOnline: online[m.UserID]}
		if m.AvatarMediaID != nil {
			a.AvatarURL = urls[m.AvatarMediaID.String()]
		}
		out[m.UserID] = a
	}
	return out
}

// noteForReply loads a note the sender may reply to inside conversation
// members: live, its author another member, and visible to the sender.
func (s *Service) noteForReply(ctx context.Context, senderID uuid.UUID, noteID uuid.UUID, memberIDs []uuid.UUID) (*postgres.Note, error) {
	ps, ok := s.parity()
	if !ok {
		return nil, fmt.Errorf("%w: notes are not supported", ErrInvalidMessageContent)
	}
	n, err := ps.GetNote(ctx, noteID, s.clock())
	if err != nil {
		return nil, err
	}
	if n == nil || n.UserID == senderID {
		return nil, fmt.Errorf("%w: note not found", ErrInvalidMessageContent)
	}
	member := false
	for _, id := range memberIDs {
		if id == n.UserID {
			member = true
		}
	}
	if !member {
		return nil, fmt.Errorf("%w: note not found", ErrInvalidMessageContent)
	}
	if n.Audience == "only_me" {
		return nil, fmt.Errorf("%w: note not found", ErrInvalidMessageContent)
	}
	rels, err := s.graphRelationships(ctx, senderID, []uuid.UUID{n.UserID})
	if err != nil {
		return nil, fmt.Errorf("%w: note not found", ErrInvalidMessageContent)
	}
	rel, known := rels[n.UserID]
	if !noteVisible(*n, rel, known) {
		return nil, fmt.Errorf("%w: note not found", ErrInvalidMessageContent)
	}
	return n, nil
}

// notes.go is Echo's server side (docs/designs/echo-contract.md): the note
// rules, the types and seams the note paths share, and the Note JSON.
//
// A note is a posts row with content_type 'note' (migration 082). Notes are
// created only by CreateNote / CreateNoteThread (notes_create.go), never by
// CreatePost: validContentTypes does not hold 'note', so POST /v1/posts
// refuses it. They are read only through the /v1/notes routes
// (notes_read.go) and the batch read feed-service hydrates timelines with;
// every other listing leaves them out (store/postgres NotNoteSQL).
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atpost/post-service/internal/actionlinks"
	"github.com/atpost/post-service/internal/premium"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// NoteContentType is the content_type of a note.
const NoteContentType = postgres.NoteContentType

// The contract's numbers.
const (
	NoteMaxRunes        = 500
	NoteMaxImages       = 4
	NoteEditWindow      = 30 * time.Minute
	NoteMinAccountAge   = 7 * 24 * time.Hour
	NoteThreadMin       = 2
	NoteThreadMax       = 25
	NoteListDefault     = 20
	NoteListMax         = 50
	noteConversationCap = 200
	noteGIFMime         = "image/gif"
)

// Reply policies.
const (
	ReplyPolicyEveryone  = "everyone"
	ReplyPolicyFollowing = "following"
	ReplyPolicyMentioned = "mentioned"
)

// ValidReplyPolicy reports whether p is one of the three policies.
func ValidReplyPolicy(p string) bool {
	switch p {
	case ReplyPolicyEveryone, ReplyPolicyFollowing, ReplyPolicyMentioned:
		return true
	}
	return false
}

// Note errors. The HTTP layer maps each to the contract's code.
var (
	ErrEchoNotEnabled           = errors.New("echo is not enabled")
	ErrEmptyNote                = errors.New("a note needs text or an image")
	ErrNoteTooLong              = errors.New("note text is longer than 500 characters")
	ErrTooManyNoteMedia         = errors.New("a note carries at most 4 images or one GIF")
	ErrNoteMediaNotAllowed      = errors.New("a note carries images or one GIF only")
	ErrPremiumRequired          = errors.New("echo posting needs Momentum Premium")
	ErrPremiumCheckUnavailable  = errors.New("premium check unavailable")
	ErrAccountTooNew            = errors.New("account is too new to reply")
	ErrAccountCheckUnavailable  = errors.New("account age check unavailable")
	ErrReplyNotAllowed          = errors.New("replies to this note are limited")
	ErrNoteNotFound             = errors.New("note not found")
	ErrInvalidNoteThread        = errors.New("invalid thread")
	ErrNoteRateLimited          = errors.New("note rate limit exceeded")
	ErrEditWindowClosed         = errors.New("a note can be edited for 30 minutes")
	ErrNotNoteAuthor            = errors.New("only the author may change this note")
	ErrInvalidNoteRequest       = errors.New("invalid note request")
	ErrInvalidNoteTab           = errors.New("invalid tab")
	ErrNoteStoreUnavailable     = errors.New("note store unavailable")
	errNoteRelationUnresolvable = errors.New("relationship unresolved")
)

// ValidateNoteText is the text rule: 1–500 runes after trim; a note with
// media may have empty text. Counted on the trimmed text, in code points
// (MaxPostTextRunes explains why not bytes).
func ValidateNoteText(text string, mediaCount int) error {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" && mediaCount == 0 {
		return ErrEmptyNote
	}
	if n := utf8.RuneCountInString(trimmed); n > NoteMaxRunes {
		return fmt.Errorf("%w: %d characters", ErrNoteTooLong, n)
	}
	return nil
}

// ValidateNoteMediaCount is the part of the media rule that needs no
// lookup: at most 4 attachments, no duplicates.
func ValidateNoteMediaCount(ids []uuid.UUID) error {
	if len(ids) > NoteMaxImages {
		return ErrTooManyNoteMedia
	}
	seen := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			return fmt.Errorf("%w: media %s is attached twice", ErrInvalidNoteRequest, id)
		}
		seen[id] = true
	}
	return nil
}

// NoteMediaRule is the media rule on the assets' kinds: images only (never
// video or audio), at most 4, and a GIF (image/gif) only on its own.
func NoteMediaRule(rows []postgres.NoteMediaRow) error {
	gifs := 0
	for _, m := range rows {
		if m.Kind != mediaKindImage {
			return fmt.Errorf("%w: %s", ErrNoteMediaNotAllowed, m.Kind)
		}
		if strings.EqualFold(strings.TrimSpace(m.MimeType), noteGIFMime) {
			gifs++
		}
	}
	if len(rows) > NoteMaxImages {
		return ErrTooManyNoteMedia
	}
	if gifs > 0 && len(rows) != 1 {
		return fmt.Errorf("%w: a GIF is attached on its own", ErrTooManyNoteMedia)
	}
	return nil
}

// noteEditWindowOpen reports whether a note created at createdAt may still
// have its text edited at now.
func noteEditWindowOpen(createdAt, now time.Time) bool {
	return !now.After(createdAt.Add(NoteEditWindow))
}

// NoteLimits are the contract's rate limits, env-tunable.
type NoteLimits struct {
	NotesPerHour       int64 // ECHO_NOTES_PER_HOUR, notes + threads + quotes
	NotesPerDay        int64 // ECHO_NOTES_PER_DAY
	RepliesPerHour     int64 // ECHO_REPLIES_PER_HOUR, Premium
	RepliesPerHourFree int64 // ECHO_REPLIES_PER_HOUR_FREE
}

// DefaultNoteLimits are the contract's values.
var DefaultNoteLimits = NoteLimits{NotesPerHour: 30, NotesPerDay: 200, RepliesPerHour: 60, RepliesPerHourFree: 20}

// NoteLimitsFromEnv reads the four variables; an unset, unparseable or
// non-positive one keeps its default.
func NoteLimitsFromEnv(getenv func(string) string) NoteLimits {
	l := DefaultNoteLimits
	read := func(key string, into *int64) {
		raw := strings.TrimSpace(getenv(key))
		if raw == "" {
			return
		}
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v <= 0 {
			slog.Warn("ignoring invalid integer env var", "key", key, "value", raw)
			return
		}
		*into = v
	}
	read("ECHO_NOTES_PER_HOUR", &l.NotesPerHour)
	read("ECHO_NOTES_PER_DAY", &l.NotesPerDay)
	read("ECHO_REPLIES_PER_HOUR", &l.RepliesPerHour)
	read("ECHO_REPLIES_PER_HOUR_FREE", &l.RepliesPerHourFree)
	return l
}

// EchoEnabledFromEnv reads ECHO_ENABLED: only "true" (any case) or "1"
// switches Echo on. Default off.
func EchoEnabledFromEnv(getenv func(string) string) bool {
	switch strings.ToLower(strings.TrimSpace(getenv("ECHO_ENABLED"))) {
	case "true", "1":
		return true
	}
	return false
}

// NoteConfig wires Echo. Every source is consulted live; a missing one
// fails closed.
type NoteConfig struct {
	Enabled bool
	// Premium reads the live premium role (internal/premium). nil: every
	// premium-gated write answers 503 PREMIUM_CHECK_UNAVAILABLE.
	Premium premium.Checker
	// Accounts reads the account's creation time (identity user-service,
	// the source the Action button already reads). nil: replies answer
	// 503 ACCOUNT_CHECK_UNAVAILABLE.
	Accounts actionlinks.AccountSource
	Limits   NoteLimits
}

// noteStore is what the note paths need from Postgres; *postgres.Store.
type noteStore interface {
	GetNotes(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*postgres.Note, error)
	GetNoteThread(ctx context.Context, rootID uuid.UUID) ([]postgres.Note, error)
	ListNotes(ctx context.Context, q postgres.NoteListQuery) ([]postgres.Note, string, error)
	NoteMedia(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]postgres.NoteMediaRow, error)
	CreateNotes(ctx context.Context, w *postgres.NoteWrite) (*postgres.NoteWriteResult, error)
	LookupCreateIdempotency(ctx context.Context, actorID uuid.UUID, key string) (uuid.UUID, string, bool, error)
	UpdateNote(ctx context.Context, id, authorID uuid.UUID, e postgres.NoteEdit) (bool, error)
	DecrementNoteReplyCount(ctx context.Context, parentID uuid.UUID) error
}

// noteRateLimiter counts one action against key; false = over limit.
type noteRateLimiter interface {
	Allow(ctx context.Context, key string, limit int64, window time.Duration) bool
}

// noteRelations answers the two reply-policy questions.
type noteRelations interface {
	// AuthorFollows reports whether author follows user. An error means
	// the graph could not answer; the caller denies.
	AuthorFollows(ctx context.Context, author, user uuid.UUID) (bool, error)
	// MentionedIDs resolves a note's mention usernames to accounts. An
	// error means a lookup failed; the caller denies.
	MentionedIDs(ctx context.Context, usernames []string) ([]uuid.UUID, error)
}

// notesState is embedded in Service.
type notesState struct {
	noteCfg       NoteConfig
	noteStoreSeam noteStore
	noteLimiter   noteRateLimiter
	noteRelSeam   noteRelations
	// noteAuthorsSeam stands in for the identity-profile batch read.
	noteAuthorsSeam func(ctx context.Context, viewerID *uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]*NoteAuthor, error)
	// noteEmitEvents makes the note paths write their outbox events with
	// no Kafka producer (tests read them off the write).
	noteEmitEvents bool
}

// SetNoteConfig wires Echo (cmd/server).
func (s *Service) SetNoteConfig(cfg NoteConfig) {
	if cfg.Limits == (NoteLimits{}) {
		cfg.Limits = DefaultNoteLimits
	}
	s.noteCfg = cfg
}

// EchoEnabled reports the ECHO_ENABLED switch.
func (s *Service) EchoEnabled() bool { return s.noteCfg.Enabled }

func (s *Service) notesStore() (noteStore, error) {
	if s.noteStoreSeam != nil {
		return s.noteStoreSeam, nil
	}
	if s.pgStore != nil {
		return s.pgStore, nil
	}
	return nil, ErrNoteStoreUnavailable
}

func (s *Service) noteLimits() NoteLimits {
	if s.noteCfg.Limits == (NoteLimits{}) {
		return DefaultNoteLimits
	}
	return s.noteCfg.Limits
}

// redisNoteLimiter is the production limiter: the post limiter's
// INCR + EXPIRE window (ratelimit.go), failing open on a Redis error the
// same way. No Redis (dev rigs) allows everything.
type redisNoteLimiter struct{ s *Service }

func (l redisNoteLimiter) Allow(ctx context.Context, key string, limit int64, window time.Duration) bool {
	if l.s.rdb == nil {
		return true
	}
	return postAllow(ctx, l.s.rdb, key, limit, window)
}

func (s *Service) noteRateLimiter() noteRateLimiter {
	if s.noteLimiter != nil {
		return s.noteLimiter
	}
	return redisNoteLimiter{s: s}
}

// checkNoteRateLimit applies the contract's limits. reply picks the reply
// budget (premium sets its size); otherwise the notes / threads / quotes
// budget, hourly then daily.
func (s *Service) checkNoteRateLimit(ctx context.Context, userID uuid.UUID, reply, isPremium bool) error {
	lim := s.noteLimits()
	rl := s.noteRateLimiter()
	if reply {
		budget := lim.RepliesPerHourFree
		if isPremium {
			budget = lim.RepliesPerHour
		}
		if !rl.Allow(ctx, "echo:rate:replies:"+userID.String()+":hour", budget, time.Hour) {
			return fmt.Errorf("%w: %d replies per hour", ErrNoteRateLimited, budget)
		}
		return nil
	}
	if !rl.Allow(ctx, "echo:rate:notes:"+userID.String()+":hour", lim.NotesPerHour, time.Hour) {
		return fmt.Errorf("%w: %d notes per hour", ErrNoteRateLimited, lim.NotesPerHour)
	}
	if !rl.Allow(ctx, "echo:rate:notes:"+userID.String()+":day", lim.NotesPerDay, 24*time.Hour) {
		return fmt.Errorf("%w: %d notes per day", ErrNoteRateLimited, lim.NotesPerDay)
	}
	return nil
}

// requirePremium is the premium gate: a live role, fail closed.
func (s *Service) requirePremium(ctx context.Context, userID uuid.UUID) error {
	if s.noteCfg.Premium == nil {
		return fmt.Errorf("%w: no premium checker configured", ErrPremiumCheckUnavailable)
	}
	ok, err := s.noteCfg.Premium.IsPremium(ctx, userID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPremiumCheckUnavailable, err)
	}
	if !ok {
		return ErrPremiumRequired
	}
	return nil
}

// isPremiumBestEffort is the reply budget's question: an unknown answer is
// the smaller budget, never a refusal (a reply does not need Premium).
func (s *Service) isPremiumBestEffort(ctx context.Context, userID uuid.UUID) bool {
	if s.noteCfg.Premium == nil {
		return false
	}
	ok, err := s.noteCfg.Premium.IsPremium(ctx, userID)
	return err == nil && ok
}

// requireAccountAge is the reply age gate: the account must be at least
// NoteMinAccountAge old. Fail closed.
func (s *Service) requireAccountAge(ctx context.Context, userID uuid.UUID) error {
	if s.noteCfg.Accounts == nil {
		return fmt.Errorf("%w: no account source configured", ErrAccountCheckUnavailable)
	}
	info, err := s.noteCfg.Accounts.Account(ctx, userID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAccountCheckUnavailable, err)
	}
	if !info.Found || !info.Active {
		return ErrAccountTooNew
	}
	if s.clock().Before(info.CreatedAt.Add(NoteMinAccountAge)) {
		return ErrAccountTooNew
	}
	return nil
}

// graphNoteRelations is the production noteRelations: graph-service's
// relationship read and user-service's username lookup.
type graphNoteRelations struct{ s *Service }

func (g graphNoteRelations) AuthorFollows(ctx context.Context, author, user uuid.UUID) (bool, error) {
	if g.s.graphServiceURL == "" {
		return false, errNoteRelationUnresolvable
	}
	rel, err := g.s.fetchRelationship(ctx, user, author)
	if err != nil || rel == nil {
		return false, errNoteRelationUnresolvable
	}
	if rel.Blocked || rel.BlockedBy {
		return false, nil
	}
	// relationship?user_id=user&other_id=author: followed_by = author
	// follows user.
	return rel.FollowedBy, nil
}

func (g graphNoteRelations) MentionedIDs(ctx context.Context, usernames []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(usernames))
	for _, r := range g.s.resolveMentions(ctx, usernames) {
		if r.err != nil {
			return nil, r.err
		}
		if r.found {
			out = append(out, r.id)
		}
	}
	return out, nil
}

func (s *Service) noteRelations() noteRelations {
	if s.noteRelSeam != nil {
		return s.noteRelSeam
	}
	return graphNoteRelations{s: s}
}

// replyPolicyAllows is the reply_policy rule for replier on note n: the
// note's author always; everyone; following = the note's author follows
// the replier; mentioned = the replier is in the note's mentions. An
// unanswerable relation denies.
func (s *Service) replyPolicyAllows(ctx context.Context, n *postgres.Note, replier uuid.UUID) bool {
	if replier == n.Post.AuthorID {
		return true
	}
	switch n.ReplyPolicy {
	case ReplyPolicyEveryone, "":
		return true
	case ReplyPolicyFollowing:
		ok, err := s.noteRelations().AuthorFollows(ctx, n.Post.AuthorID, replier)
		return err == nil && ok
	case ReplyPolicyMentioned:
		if len(n.Post.MentionUsernames) == 0 {
			return false
		}
		ids, err := s.noteRelations().MentionedIDs(ctx, n.Post.MentionUsernames)
		if err != nil {
			return false
		}
		for _, id := range ids {
			if id == replier {
				return true
			}
		}
		return false
	}
	return false
}

// NoteAuthor is the author block of a note.
type NoteAuthor struct {
	ID                uuid.UUID `json:"id"`
	Username          string    `json:"username"`
	DisplayName       string    `json:"display_name"`
	AvatarURL         string    `json:"avatar_url"`
	IsVerified        bool      `json:"is_verified"`
	VerificationLevel string    `json:"verification_level"`
	BadgeFlags        int       `json:"badge_flags"`
}

// NoteView is the Note JSON: the post JSON GET /v1/posts/:id returns plus
// the contract's fields, which are always present (the outer fields
// shadow the post's own like_count, repost_count, author and media).
type NoteView struct {
	*PostDetail
	Media             []postgres.PostMedia `json:"media"`
	EditedAt          *time.Time           `json:"edited_at"`
	ReplyToID         *uuid.UUID           `json:"reply_to_id"`
	ConversationID    uuid.UUID            `json:"conversation_id"`
	QuoteOfID         *uuid.UUID           `json:"quote_of_id"`
	ReplyPolicy       string               `json:"reply_policy"`
	ReplyCount        int                  `json:"reply_count"`
	LikeCount         int64                `json:"like_count"`
	RepostCount       int                  `json:"repost_count"`
	ViewerHasLiked    bool                 `json:"viewer_has_liked"`
	ViewerHasReposted bool                 `json:"viewer_has_reposted"`
	ViewerCanReply    bool                 `json:"viewer_can_reply"`
	Author            *NoteAuthor          `json:"author,omitempty"`
	QuotedNote        *NoteView            `json:"quoted_note"`
}

// NotePage is a listing's answer.
type NotePage struct {
	Items      []*NoteView `json:"items"`
	NextCursor string      `json:"next_cursor"`
}

// NoteEligibility is GET /v1/notes/me/eligibility.
type NoteEligibility struct {
	CanPost  bool   `json:"can_post"`
	CanReply bool   `json:"can_reply"`
	Reason   string `json:"reason"`
}

// Eligibility reasons.
const (
	EligibilityPremiumRequired = "premium_required"
	EligibilityAccountTooNew   = "account_too_new"
	EligibilitySuspended       = "suspended"
)

// buildNoteView joins a hydrated post with its note columns.
func buildNoteView(d *PostDetail, n *postgres.Note) *NoteView {
	v := &NoteView{
		PostDetail:  d,
		Media:       d.Post.Media,
		EditedAt:    n.EditedAt,
		ReplyToID:   n.ReplyToID,
		QuoteOfID:   n.QuoteOfID,
		ReplyPolicy: n.ReplyPolicy,
		ReplyCount:  n.ReplyCount,
		RepostCount: d.RepostCount,
	}
	if v.Media == nil {
		v.Media = []postgres.PostMedia{}
	}
	if v.ReplyPolicy == "" {
		v.ReplyPolicy = ReplyPolicyEveryone
	}
	v.ConversationID = d.Post.ID
	if n.ConversationID != nil {
		v.ConversationID = *n.ConversationID
	}
	if d.Counts != nil {
		v.LikeCount = d.Counts.Likes
	}
	if d.LikeCount != nil && !d.LikeCount.Hidden {
		v.LikeCount = d.LikeCount.Value
	}
	v.ViewerHasLiked = d.HasReacted
	v.ViewerHasReposted = d.HasReposted
	return v
}

// noteAuthors hydrates the author blocks for ids, best-effort: an error or
// no profile service leaves them absent (the contract's fallback).
func (s *Service) noteAuthors(ctx context.Context, viewerID *uuid.UUID, ids []uuid.UUID) map[uuid.UUID]*NoteAuthor {
	if len(ids) == 0 {
		return nil
	}
	if s.noteAuthorsSeam != nil {
		m, err := s.noteAuthorsSeam(ctx, viewerID, ids)
		if err != nil {
			return nil
		}
		return m
	}
	if s.profileServiceURL == "" {
		return nil
	}
	strs := make([]string, len(ids))
	for i, id := range ids {
		strs[i] = id.String()
	}
	profiles, err := s.fetchNoteProfiles(ctx, viewerID, strs)
	if err != nil {
		slog.Debug("note author hydration skipped", "err", err)
		return nil
	}
	out := make(map[uuid.UUID]*NoteAuthor, len(ids))
	for _, id := range ids {
		pr, ok := profiles[id]
		if !ok {
			out[id] = &NoteAuthor{ID: id, DisplayName: "Deleted account"}
			continue
		}
		a := &NoteAuthor{ID: id, Username: pr.Username, DisplayName: pr.DisplayName,
			IsVerified: pr.IsVerified, VerificationLevel: pr.VerificationLevel, BadgeFlags: pr.BadgeFlags}
		if pr.AvatarMediaID != nil {
			a.AvatarURL = fmt.Sprintf("/v1/media/%s/serve/avatar", pr.AvatarMediaID)
		}
		out[id] = a
	}
	return out
}

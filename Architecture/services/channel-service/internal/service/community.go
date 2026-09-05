package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atpost/channel-service/internal/store"
	"github.com/google/uuid"
)

// Chat-app pass (2026-09-05): a COMMUNITY is a broadcast channel. Anyone can
// create one; users join (subscribe); ONLY the owner/admins post updates;
// members cannot reply (comments are disabled by default and refused while
// disabled) — emoji reactions are the only member-side write.
//
// Wire vocabulary used by Android:
//   visibility  public|private  ⇄ channel_type (public ⇄ "public",
//                                private ⇄ "private"; legacy types read as
//                                public unless private/paid)
//   member_count               = subscriber_count (owner counts)
//   viewer_role                owner|admin|subscriber|banned|"" (not joined)

const (
	communityNameMaxRunes  = 60
	communityAboutMaxRunes = 300
	updateBodyMaxRunes     = 2000
	eventTitleMaxRunes     = 120
	eventLocationMaxRunes  = 200
	reactionEmojiMaxRunes  = 8
	reportReasonMaxRunes   = 64
	reportDetailsMaxRunes  = 1000
	reportsPerHour         = 20
)

var handlePattern = regexp.MustCompile(`^[a-z0-9_]{3,30}$`)

// muteForever is what "mute" without an end time stores; unmute clears it.
var muteForever = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

// VisibilityOf maps the storage channel_type onto the two-value wire field.
func VisibilityOf(channelType string) string {
	switch channelType {
	case "private", "paid":
		return "private"
	default:
		return "public"
	}
}

// channelTypeForVisibility maps the wire field back. "" keeps current.
func channelTypeForVisibility(visibility string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(visibility)) {
	case "":
		return "", nil
	case "public":
		return "public", nil
	case "private":
		return "private", nil
	default:
		return "", fmt.Errorf("invalid: visibility must be public or private")
	}
}

func validateCommunityName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("invalid: name is required")
	}
	if utf8.RuneCountInString(name) > communityNameMaxRunes {
		return "", fmt.Errorf("invalid: name must be at most %d characters", communityNameMaxRunes)
	}
	return name, nil
}

func validateCommunityAbout(about string) (string, error) {
	about = strings.TrimSpace(about)
	if utf8.RuneCountInString(about) > communityAboutMaxRunes {
		return "", fmt.Errorf("invalid: about must be at most %d characters", communityAboutMaxRunes)
	}
	return about, nil
}

func normalizeHandle(handle string) (string, error) {
	handle = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(handle), "@"))
	if !handlePattern.MatchString(handle) {
		return "", fmt.Errorf("invalid: handle must be 3-30 characters of a-z, 0-9 or _")
	}
	return handle, nil
}

// EventInfo is the optional event attached to an update (update_type=event).
type EventInfo struct {
	Title    string     `json:"title"`
	StartsAt time.Time  `json:"starts_at"`
	EndsAt   *time.Time `json:"ends_at,omitempty"`
	Location string     `json:"location,omitempty"`
}

func validateEvent(e *EventInfo) error {
	e.Title = strings.TrimSpace(e.Title)
	e.Location = strings.TrimSpace(e.Location)
	if e.Title == "" {
		return fmt.Errorf("invalid: event.title is required")
	}
	if utf8.RuneCountInString(e.Title) > eventTitleMaxRunes {
		return fmt.Errorf("invalid: event.title must be at most %d characters", eventTitleMaxRunes)
	}
	if e.StartsAt.IsZero() {
		return fmt.Errorf("invalid: event.starts_at is required")
	}
	if e.EndsAt != nil && e.EndsAt.Before(e.StartsAt) {
		return fmt.Errorf("invalid: event.ends_at must not be before starts_at")
	}
	if utf8.RuneCountInString(e.Location) > eventLocationMaxRunes {
		return fmt.Errorf("invalid: event.location must be at most %d characters", eventLocationMaxRunes)
	}
	return nil
}

// eventFromMetadata reads {"event": {...}} out of an update's metadata.
func eventFromMetadata(raw json.RawMessage) *EventInfo {
	if len(raw) == 0 {
		return nil
	}
	var meta struct {
		Event *EventInfo `json:"event"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil || meta.Event == nil || meta.Event.Title == "" {
		return nil
	}
	return meta.Event
}

// mergeEventMetadata writes the event into the metadata object, keeping any
// other keys the caller sent.
func mergeEventMetadata(raw json.RawMessage, e *EventInfo) (json.RawMessage, error) {
	meta := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &meta); err != nil {
			return nil, fmt.Errorf("invalid: metadata must be a JSON object")
		}
	}
	meta["event"] = e
	return json.Marshal(meta)
}

// UpdateView is a ChannelUpdate decorated for the viewer.
type UpdateView struct {
	*store.ChannelUpdate
	Event          *EventInfo            `json:"event,omitempty"`
	Reactions      []store.ReactionCount `json:"reactions"`
	ViewerReaction string                `json:"viewer_reaction,omitempty"`
	// CanEdit is true for the owner/admins — the client hides the composer
	// and edit affordances for everyone else.
	CanEdit bool `json:"can_edit"`
}

func (s *Service) decorateUpdates(ctx context.Context, viewerID *uuid.UUID, canEdit bool, updates []store.ChannelUpdate) []UpdateView {
	ids := make([]uuid.UUID, len(updates))
	for i := range updates {
		ids[i] = updates[i].ID
	}
	counts, err := s.store.GetReactionCounts(ctx, ids)
	if err != nil {
		slog.Warn("reaction counts lookup failed", "error", err)
		counts = map[uuid.UUID][]store.ReactionCount{}
	}
	mine := map[uuid.UUID]string{}
	if viewerID != nil {
		if m, err := s.store.GetViewerReactions(ctx, *viewerID, ids); err == nil {
			mine = m
		}
	}
	out := make([]UpdateView, len(updates))
	for i := range updates {
		u := &updates[i]
		rc := counts[u.ID]
		if rc == nil {
			rc = []store.ReactionCount{}
		}
		out[i] = UpdateView{
			ChannelUpdate:  u,
			Event:          eventFromMetadata(u.Metadata),
			Reactions:      rc,
			ViewerReaction: mine[u.ID],
			CanEdit:        canEdit,
		}
	}
	return out
}

// viewerCanPost reports owner/admin standing.
func (s *Service) viewerCanPost(ctx context.Context, ch *store.BroadcastChannel, viewerID *uuid.UUID) bool {
	if viewerID == nil {
		return false
	}
	if ch.OwnerID == *viewerID {
		return true
	}
	return isAtLeast(s.store.GetMemberRole(ctx, ch.ID, *viewerID), "admin")
}

// requireOwner loads the channel and asserts the actor owns it.
func (s *Service) requireOwner(ctx context.Context, channelID, actorID uuid.UUID) (*store.BroadcastChannel, error) {
	ch, err := s.store.GetChannelByID(ctx, channelID)
	if err != nil {
		return nil, err
	}
	if ch.OwnerID != actorID {
		return nil, fmt.Errorf("forbidden: only the channel owner can manage admins")
	}
	return ch, nil
}

// requireAdmin loads the channel and asserts owner/admin standing.
func (s *Service) requireAdmin(ctx context.Context, channelID, actorID uuid.UUID) (*store.BroadcastChannel, error) {
	ch, err := s.store.GetChannelByID(ctx, channelID)
	if err != nil {
		return nil, err
	}
	if ch.OwnerID == actorID {
		return ch, nil
	}
	member, err := s.store.GetMember(ctx, channelID, actorID)
	if err != nil {
		return nil, fmt.Errorf("failed to check membership: %w", err)
	}
	if member == nil || !isAtLeast(member.Role, "admin") {
		return nil, fmt.Errorf("forbidden: only admins can do this")
	}
	return ch, nil
}

// --- Admins ---

// ListAdmins is visible to anyone who may view the channel.
func (s *Service) ListAdmins(ctx context.Context, channelID uuid.UUID, viewerID *uuid.UUID) ([]store.ChannelMember, error) {
	if err := s.authorizeViewer(ctx, channelID, viewerID); err != nil {
		return nil, err
	}
	return s.store.ListAdmins(ctx, channelID)
}

// AddAdmin promotes a member to admin (owner only). A non-member is joined
// and promoted in one step.
func (s *Service) AddAdmin(ctx context.Context, channelID, actorID, targetID uuid.UUID) error {
	ch, err := s.requireOwner(ctx, channelID, actorID)
	if err != nil {
		return err
	}
	if targetID == ch.OwnerID {
		return fmt.Errorf("invalid: the owner is already above admin")
	}
	member, err := s.store.GetMember(ctx, channelID, targetID)
	if err != nil {
		return fmt.Errorf("failed to check membership: %w", err)
	}
	if member != nil && member.Role == "banned" {
		return fmt.Errorf("invalid: unban the user before promoting them")
	}
	if member == nil {
		inserted, err := s.store.AddMember(ctx, &store.ChannelMember{ChannelID: channelID, UserID: targetID, Role: "admin", NotifyOn: "all"})
		if err != nil {
			return fmt.Errorf("failed to add admin: %w", err)
		}
		if inserted {
			if err := s.adjustSubscriberCount(ctx, channelID, 1); err != nil {
				slog.Warn("failed to increment subscriber count", "error", err)
			}
		}
		return nil
	}
	if member.Role == "admin" {
		return nil // idempotent
	}
	return s.store.UpdateMemberRole(ctx, channelID, targetID, "admin")
}

// RemoveAdmin demotes an admin back to subscriber (owner only). Idempotent.
func (s *Service) RemoveAdmin(ctx context.Context, channelID, actorID, targetID uuid.UUID) error {
	ch, err := s.requireOwner(ctx, channelID, actorID)
	if err != nil {
		return err
	}
	if targetID == ch.OwnerID {
		return fmt.Errorf("invalid: the owner cannot be demoted; transfer or delete the channel")
	}
	member, err := s.store.GetMember(ctx, channelID, targetID)
	if err != nil {
		return fmt.Errorf("failed to check membership: %w", err)
	}
	if member == nil || member.Role != "admin" {
		return nil
	}
	return s.store.UpdateMemberRole(ctx, channelID, targetID, "subscriber")
}

// --- Mute ---

// Mute silences the community for the member. mutedUntil nil = indefinitely.
func (s *Service) Mute(ctx context.Context, channelID, userID uuid.UUID, mutedUntil *time.Time) error {
	member, err := s.store.GetMember(ctx, channelID, userID)
	if err != nil {
		return fmt.Errorf("failed to check membership: %w", err)
	}
	if member == nil {
		return fmt.Errorf("not a member of this channel")
	}
	until := muteForever
	if mutedUntil != nil {
		if !mutedUntil.After(time.Now()) {
			return fmt.Errorf("invalid: muted_until must be in the future")
		}
		until = *mutedUntil
	}
	return s.store.SetMutedUntil(ctx, channelID, userID, &until)
}

// Unmute clears the mute. Idempotent for members; non-members get not found.
func (s *Service) Unmute(ctx context.Context, channelID, userID uuid.UUID) error {
	member, err := s.store.GetMember(ctx, channelID, userID)
	if err != nil {
		return fmt.Errorf("failed to check membership: %w", err)
	}
	if member == nil {
		return fmt.Errorf("not a member of this channel")
	}
	return s.store.SetMutedUntil(ctx, channelID, userID, nil)
}

func memberIsMuted(m *store.ChannelMember) bool {
	return m != nil && m.MutedUntil != nil && m.MutedUntil.After(time.Now())
}

// --- Reactions ---

// ReactToUpdate sets the viewer's single emoji (replacing any previous one).
func (s *Service) ReactToUpdate(ctx context.Context, channelID, updateID, userID uuid.UUID, emoji string) (*UpdateView, error) {
	emoji = strings.TrimSpace(emoji)
	if emoji == "" || utf8.RuneCountInString(emoji) > reactionEmojiMaxRunes || strings.ContainsAny(emoji, " \t\r\n") {
		return nil, fmt.Errorf("invalid: emoji is required (at most %d characters, no whitespace)", reactionEmojiMaxRunes)
	}
	if err := s.authorizeEngagement(ctx, channelID, userID); err != nil {
		return nil, err
	}
	ch, err := s.store.GetChannelByID(ctx, channelID)
	if err != nil {
		return nil, err
	}
	if ch.ReactionMode == "disabled" {
		return nil, fmt.Errorf("forbidden: reactions are disabled on this channel")
	}
	u, err := s.store.GetUpdate(ctx, updateID)
	if err != nil {
		return nil, err
	}
	if u.ChannelID != channelID || u.Status != "published" {
		return nil, fmt.Errorf("update not found")
	}
	if _, err := s.store.SetReaction(ctx, updateID, userID, emoji); err != nil {
		return nil, err
	}
	return s.viewUpdate(ctx, ch, updateID, &userID)
}

// UnreactToUpdate removes the viewer's reaction. Idempotent.
func (s *Service) UnreactToUpdate(ctx context.Context, channelID, updateID, userID uuid.UUID) (*UpdateView, error) {
	if err := s.authorizeEngagement(ctx, channelID, userID); err != nil {
		return nil, err
	}
	ch, err := s.store.GetChannelByID(ctx, channelID)
	if err != nil {
		return nil, err
	}
	u, err := s.store.GetUpdate(ctx, updateID)
	if err != nil {
		return nil, err
	}
	if u.ChannelID != channelID {
		return nil, fmt.Errorf("update not found")
	}
	if _, err := s.store.RemoveReaction(ctx, updateID, userID); err != nil {
		return nil, err
	}
	return s.viewUpdate(ctx, ch, updateID, &userID)
}

// GetUpdate returns one decorated update for the viewer.
func (s *Service) GetUpdate(ctx context.Context, channelID, updateID uuid.UUID, viewerID *uuid.UUID) (*UpdateView, error) {
	if err := s.authorizeViewer(ctx, channelID, viewerID); err != nil {
		return nil, err
	}
	ch, err := s.store.GetChannelByID(ctx, channelID)
	if err != nil {
		return nil, err
	}
	return s.viewUpdate(ctx, ch, updateID, viewerID)
}

func (s *Service) viewUpdate(ctx context.Context, ch *store.BroadcastChannel, updateID uuid.UUID, viewerID *uuid.UUID) (*UpdateView, error) {
	u, err := s.store.GetUpdate(ctx, updateID)
	if err != nil {
		return nil, err
	}
	if u.ChannelID != ch.ID {
		return nil, fmt.Errorf("update not found")
	}
	if u.Status != "published" && !s.viewerCanPost(ctx, ch, viewerID) {
		return nil, fmt.Errorf("update not found")
	}
	views := s.decorateUpdates(ctx, viewerID, s.viewerCanPost(ctx, ch, viewerID), []store.ChannelUpdate{*u})
	return &views[0], nil
}

// --- Reports ---

// Report files a report on the channel (updateID nil) or on one update.
func (s *Service) Report(ctx context.Context, channelID uuid.UUID, updateID *uuid.UUID, reporterID uuid.UUID, reason, details string) (*store.ChannelReport, error) {
	reason = strings.TrimSpace(strings.ToLower(reason))
	details = strings.TrimSpace(details)
	if reason == "" || utf8.RuneCountInString(reason) > reportReasonMaxRunes {
		return nil, fmt.Errorf("invalid: reason is required (at most %d characters)", reportReasonMaxRunes)
	}
	if utf8.RuneCountInString(details) > reportDetailsMaxRunes {
		return nil, fmt.Errorf("invalid: details must be at most %d characters", reportDetailsMaxRunes)
	}
	if err := s.authorizeViewer(ctx, channelID, &reporterID); err != nil {
		return nil, err
	}
	if updateID != nil {
		u, err := s.store.GetUpdate(ctx, *updateID)
		if err != nil {
			return nil, err
		}
		if u.ChannelID != channelID {
			return nil, fmt.Errorf("update not found")
		}
	}
	n, err := s.store.CountRecentReportsBy(ctx, reporterID, time.Now().Add(-time.Hour))
	if err != nil {
		return nil, err
	}
	if n >= reportsPerHour {
		return nil, fmt.Errorf("rate_limited: too many reports, try again later")
	}
	return s.store.CreateReport(ctx, channelID, updateID, reporterID, reason, details)
}

// --- Notifications: immediate publishes reach the fan-out worker ---

// emitFanout hands a freshly published update to the fan-out worker
// (atpost.channel.updates), which is what reaches subscribers' inboxes and
// devices through notification-service. Scheduled updates already take this
// path from the scheduler; immediate publishes previously only emitted the
// analytics event on channel-events and never notified anyone.
func (s *Service) emitFanout(ctx context.Context, ch *store.BroadcastChannel, u *store.ChannelUpdate) {
	if s.producer == nil {
		return
	}
	if err := s.producer.PublishFanoutUpdate(ctx, ch.ID, ch.Name, u.ID, u.AuthorID, u.UpdateType, u.Title, u.Body); err != nil {
		slog.Warn("failed to emit channel update fan-out", "update_id", u.ID, "error", err)
	}
}

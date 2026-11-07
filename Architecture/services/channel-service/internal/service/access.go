package service

import (
	"context"
	"errors"

	"github.com/atpost/channel-service/internal/store"
	"github.com/google/uuid"
)

// ─── THE CHANNEL ACCESS GATE (audit 2026-11-07, CH-M1) ──────────────────
//
// Every read and every member-side write of a channel, and of anything that
// hangs off one of its updates (comments, the comment delta, polls, RSVPs,
// attendees, reactions, sparks, stashes, echoes, views), goes through
// authorize and, for update children, gateUpdate. Before this file the
// engagement routes had one gate, the read routes another, and the comment,
// poll, RSVP and attendee routes none at all: a member banned from (or
// removed from) a private community kept reading its threads, voting and
// RSVPing with the update ids they had seen, and `/comments/delta` returned
// any update's comments under any channel id.
//
// The decision is pure (decideAccess, updateVisible) so it is tested without
// a database; the wiring is pinned on the source (access_test.go).

// accessMode says what the caller is about to do.
type accessMode int

const (
	// accessRead: list or read. Anonymous callers allowed on public channels.
	accessRead accessMode = iota
	// accessWrite: a member-side write (comment, vote, RSVP, reaction...).
	// Needs an authenticated caller.
	accessWrite
	// accessReport: filing a report. Same as a read, except that a ban on a
	// PUBLIC community does not silence a report about it — a ban must not
	// become a way to stop someone reporting the people who banned them.
	accessReport
)

// ErrUpdateNotFound is every "this update is not yours to see" answer: an
// update of another channel, a deleted one, or an unpublished one seen by
// someone who cannot post. The handler maps "not found" to 404.
var ErrUpdateNotFound = errors.New("update not found")

// Membership refusals added with the 2026-11-07 audit fixes.
var (
	// ErrAdminMustBeMember (CH-L3): only someone who joined on their own
	// can be made an admin, so the join gates (pilot allowlist, consent)
	// cannot be skipped by a promotion.
	ErrAdminMustBeMember = errors.New("invalid: only an existing member can be made an admin; they must join first")
	// ErrMemberIsBanned (CH-M3): removing a banned member would erase the
	// ban; unban instead.
	ErrMemberIsBanned = errors.New("invalid: this user is banned; unban them instead of removing them")
)

// decideAccess is the whole channel-level decision.
//
//	not active (suspended, archived)   -> not found, for everyone
//	the owner                          -> allowed
//	private/paid, not a member/banned  -> not found: a closed community is
//	                                      hidden, never confirmed (founder rule;
//	                                      same answer as GET /{id})
//	banned                             -> banned (except accessReport)
//	write without a caller             -> not a subscriber
//
// role is the caller's channel_members role, "" for none or anonymous.
func decideAccess(ch *store.BroadcastChannel, viewerID *uuid.UUID, role string, mode accessMode) error {
	if ch == nil || !channelIsServable(ch.Status) {
		return ErrChannelNotFound
	}
	if viewerID != nil && ch.OwnerID == *viewerID {
		return nil
	}
	if VisibilityOf(ch.ChannelType) == "private" && (role == "" || role == "banned") {
		return ErrChannelNotFound
	}
	if role == "banned" && mode != accessReport {
		return ErrChannelMemberBanned
	}
	if mode == accessWrite && viewerID == nil {
		return ErrChannelNotSubscriber
	}
	return nil
}

// effectiveRole is the caller's standing: the owner outranks the member row,
// which can be missing for the owner in older databases.
func effectiveRole(ch *store.BroadcastChannel, viewerID *uuid.UUID, role string) string {
	if viewerID != nil && ch != nil && ch.OwnerID == *viewerID {
		return "owner"
	}
	return role
}

// updateVisible is the update-level half: the update must belong to the
// channel in the path (never trust the pairing of two path ids), and an
// unpublished one is visible only to those who can post.
func updateVisible(u *store.ChannelUpdate, channelID uuid.UUID, role string) error {
	if u == nil || u.ChannelID != channelID {
		return ErrUpdateNotFound
	}
	if u.Status != "published" && !isAtLeast(role, "admin") {
		return ErrUpdateNotFound
	}
	return nil
}

// authorize loads the channel and the caller's role and applies
// decideAccess. The role comes back as effectiveRole. A database error on
// the role lookup fails CLOSED: it is returned, never read as "not a
// member" (which on a public channel would admit a banned caller).
func (s *Service) authorize(ctx context.Context, channelID uuid.UUID, viewerID *uuid.UUID, mode accessMode) (*store.BroadcastChannel, string, error) {
	ch, err := s.store.GetChannelByID(ctx, channelID)
	if err != nil {
		return nil, "", err
	}
	role := ""
	if viewerID != nil {
		if role, err = s.store.MemberRole(ctx, channelID, *viewerID); err != nil {
			return nil, "", err
		}
	}
	if err := decideAccess(ch, viewerID, role, mode); err != nil {
		return nil, "", err
	}
	return ch, effectiveRole(ch, viewerID, role), nil
}

// updateAccess is what a route acting on one update gets back from the gate.
type updateAccess struct {
	ch   *store.BroadcastChannel
	u    *store.ChannelUpdate
	role string // effectiveRole: "owner" for the owner, "" for none
}

// gateUpdate is authorize plus updateVisible: the one entry point for every
// route under /{channelId}/updates/{updateId}/.
func (s *Service) gateUpdate(ctx context.Context, channelID, updateID uuid.UUID, viewerID *uuid.UUID, mode accessMode) (*updateAccess, error) {
	ch, role, err := s.authorize(ctx, channelID, viewerID, mode)
	if err != nil {
		return nil, err
	}
	u, err := s.store.GetUpdate(ctx, updateID)
	if err != nil {
		return nil, err
	}
	if err := updateVisible(u, channelID, role); err != nil {
		return nil, err
	}
	return &updateAccess{ch: ch, u: u, role: role}, nil
}

package service

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/channel-service/internal/store"
	"github.com/google/uuid"
)

// Audit 2026-11-07, channel-service. Each test names the finding it defends.

func gateChannel(channelType, status string, owner uuid.UUID) *store.BroadcastChannel {
	return &store.BroadcastChannel{ID: uuid.New(), OwnerID: owner, ChannelType: channelType, Status: status}
}

// CH-M1: the one channel-level decision every read and write goes through.
func TestDecideAccess(t *testing.T) {
	owner, viewer := uuid.New(), uuid.New()
	me := &viewer
	for _, tc := range []struct {
		name   string
		ch     *store.BroadcastChannel
		viewer *uuid.UUID
		role   string
		mode   accessMode
		want   error
	}{
		{"missing channel", nil, me, "subscriber", accessRead, ErrChannelNotFound},
		{"suspended, even for the owner", gateChannel("public", "suspended", owner), &owner, "owner", accessRead, ErrChannelNotFound},
		{"archived", gateChannel("public", "archived", owner), me, "subscriber", accessRead, ErrChannelNotFound},
		{"owner of a private channel without a member row", gateChannel("private", "active", owner), &owner, "", accessWrite, nil},

		{"public, anonymous read", gateChannel("public", "active", owner), nil, "", accessRead, nil},
		{"public, anonymous write", gateChannel("public", "active", owner), nil, "", accessWrite, ErrChannelNotSubscriber},
		{"public, non-member read", gateChannel("public", "active", owner), me, "", accessRead, nil},
		{"public, non-member write", gateChannel("public", "active", owner), me, "", accessWrite, nil},
		{"public, banned read", gateChannel("public", "active", owner), me, "banned", accessRead, ErrChannelMemberBanned},
		{"public, banned write", gateChannel("public", "active", owner), me, "banned", accessWrite, ErrChannelMemberBanned},
		{"public, banned may still report", gateChannel("public", "active", owner), me, "banned", accessReport, nil},
		{"creator type is public", gateChannel("creator", "active", owner), me, "banned", accessRead, ErrChannelMemberBanned},

		{"private, member read", gateChannel("private", "active", owner), me, "subscriber", accessRead, nil},
		{"private, member write", gateChannel("private", "active", owner), me, "subscriber", accessWrite, nil},
		{"private, non-member is hidden", gateChannel("private", "active", owner), me, "", accessRead, ErrChannelNotFound},
		{"private, anonymous is hidden", gateChannel("private", "active", owner), nil, "", accessRead, ErrChannelNotFound},
		{"private, banned is hidden", gateChannel("private", "active", owner), me, "banned", accessWrite, ErrChannelNotFound},
		{"private, banned report is hidden", gateChannel("private", "active", owner), me, "banned", accessReport, ErrChannelNotFound},
		{"paid is private", gateChannel("paid", "active", owner), me, "", accessRead, ErrChannelNotFound},
	} {
		if got := decideAccess(tc.ch, tc.viewer, tc.role, tc.mode); !errors.Is(got, tc.want) {
			t.Errorf("%s: decideAccess = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestEffectiveRole(t *testing.T) {
	owner, other := uuid.New(), uuid.New()
	ch := gateChannel("public", "active", owner)
	if got := effectiveRole(ch, &owner, ""); got != "owner" {
		t.Fatalf("the owner without a member row = %q, want owner", got)
	}
	if got := effectiveRole(ch, &other, "admin"); got != "admin" {
		t.Fatalf("an admin = %q, want admin", got)
	}
	if got := effectiveRole(ch, nil, ""); got != "" {
		t.Fatalf("anonymous = %q, want empty", got)
	}
}

// CH-M1: the update must belong to the channel in the path; an unpublished
// one is for those who can post.
func TestUpdateVisible(t *testing.T) {
	channelID := uuid.New()
	published := &store.ChannelUpdate{ChannelID: channelID, Status: "published"}
	scheduled := &store.ChannelUpdate{ChannelID: channelID, Status: "scheduled"}
	elsewhere := &store.ChannelUpdate{ChannelID: uuid.New(), Status: "published"}
	for _, tc := range []struct {
		name string
		u    *store.ChannelUpdate
		role string
		want error
	}{
		{"published, anyone", published, "", nil},
		{"another channel's update", elsewhere, "owner", ErrUpdateNotFound},
		{"no update", nil, "owner", ErrUpdateNotFound},
		{"scheduled, subscriber", scheduled, "subscriber", ErrUpdateNotFound},
		{"scheduled, moderator", scheduled, "moderator", ErrUpdateNotFound},
		{"scheduled, admin", scheduled, "admin", nil},
		{"scheduled, owner", scheduled, "owner", nil},
	} {
		if got := updateVisible(tc.u, channelID, tc.role); !errors.Is(got, tc.want) {
			t.Errorf("%s: updateVisible = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestCommentAllowed(t *testing.T) {
	if err := commentAllowed("disabled", "owner"); err == nil {
		t.Fatal("comment_mode=disabled must refuse everyone, the owner included")
	}
	if err := commentAllowed("subscribers_only", ""); err == nil {
		t.Fatal("subscribers_only must refuse a non-member")
	}
	if err := commentAllowed("subscribers_only", "owner"); err != nil {
		t.Fatalf("subscribers_only must accept the owner: %v", err)
	}
	if err := commentAllowed("enabled", ""); err != nil {
		t.Fatalf("enabled must accept a public non-member: %v", err)
	}
}

// CH-M3: leaving never deletes a ban.
func TestGuardLeave(t *testing.T) {
	if _, err := guardLeave(nil); err == nil {
		t.Fatal("a non-member cannot leave")
	}
	if _, err := guardLeave(&store.ChannelMember{Role: "owner"}); err == nil {
		t.Fatal("the owner cannot leave")
	}
	remove, err := guardLeave(&store.ChannelMember{Role: "banned"})
	if err != nil || remove {
		t.Fatalf("a banned member leaving = (%v, %v), want (false, nil): the ban row must stay", remove, err)
	}
	for _, role := range []string{"subscriber", "admin", "moderator", "editor"} {
		if remove, err := guardLeave(&store.ChannelMember{Role: role}); err != nil || !remove {
			t.Fatalf("%s leaving = (%v, %v), want (true, nil)", role, remove, err)
		}
	}
}

// CH-L3: only an existing, non-banned member can be promoted.
func TestGuardPromote(t *testing.T) {
	if _, err := guardPromote(nil); !errors.Is(err, ErrAdminMustBeMember) {
		t.Fatalf("promoting a non-member = %v, want ErrAdminMustBeMember", err)
	}
	if _, err := guardPromote(&store.ChannelMember{Role: "banned"}); err == nil {
		t.Fatal("promoting a banned member must be refused")
	}
	if promote, err := guardPromote(&store.ChannelMember{Role: "owner"}); err == nil || promote {
		t.Fatal("an owner row must never be rewritten to admin")
	}
	if promote, err := guardPromote(&store.ChannelMember{Role: "admin"}); err != nil || promote {
		t.Fatalf("an admin = (%v, %v), want idempotent (false, nil)", promote, err)
	}
	if promote, err := guardPromote(&store.ChannelMember{Role: "subscriber"}); err != nil || !promote {
		t.Fatalf("a subscriber = (%v, %v), want (true, nil)", promote, err)
	}
}

// CH-L1: the per-row decoration is pure now; the queries are batched.
func TestDecorateChannel(t *testing.T) {
	owner, viewer := uuid.New(), uuid.New()
	ch := gateChannel("private", "active", owner)
	ch.SubscriberCount = 99
	future := time.Now().Add(time.Hour)

	got := decorateChannel(ch, &viewer, &store.ChannelMember{Role: "admin", MutedUntil: &future}, 7, true)
	if got.MemberCount != 7 || got.ViewerRole != "admin" || !got.ViewerMuted || !got.CanPost || got.Visibility != "private" {
		t.Fatalf("admin decoration: %+v", got)
	}
	if got := decorateChannel(ch, &owner, nil, 0, true); got.ViewerRole != "owner" || !got.CanPost || got.MemberCount != 0 {
		t.Fatalf("owner without a row: %+v", got)
	}
	if got := decorateChannel(ch, nil, nil, 0, false); got.MemberCount != 99 || got.ViewerRole != "" {
		t.Fatalf("uncounted falls back to the stored count: %+v", got)
	}
}

// CH-M1 wiring: the gate itself needs a database, so the call is pinned on
// the source — every route under an update starts with gateUpdate in the
// right mode, before it touches the store.
func TestEveryUpdateChildRouteRunsTheGateFirst(t *testing.T) {
	src := readSources(t, "channel.go", "community.go")
	const (
		write    = "s.gateUpdate(ctx, channelID, updateID, &userID, accessWrite)"
		read     = "s.gateUpdate(ctx, channelID, updateID, viewerID, accessRead)"
		readSelf = "s.gateUpdate(ctx, channelID, updateID, &userID, accessRead)"
		chanRead = "s.authorize(ctx, channelID, viewerID, accessRead)"
	)
	for sig, gate := range map[string]string{
		"func (s *Service) SparkUpdate(":       write,
		"func (s *Service) UnsparkUpdate(":     write,
		"func (s *Service) StashUpdate(":       write,
		"func (s *Service) UnstashUpdate(":     write,
		"func (s *Service) EchoUpdate(":        write,
		"func (s *Service) UnechoUpdate(":      write,
		"func (s *Service) RecordView(":        write,
		"func (s *Service) AddComment(":        write,
		"func (s *Service) VoteOnPoll(":        write,
		"func (s *Service) RSVPEvent(":         write,
		"func (s *Service) ReactToUpdate(":     write,
		"func (s *Service) UnreactToUpdate(":   write,
		"func (s *Service) ListComments(":      read,
		"func (s *Service) ListCommentsSince(": read,
		"func (s *Service) GetPollResults(":    read,
		"func (s *Service) ListAttendees(":     read,
		"func (s *Service) DeleteComment(":     readSelf,
		"func (s *Service) PinComment(":        readSelf,
		"func (s *Service) ListUpdates(":       chanRead,
		"func (s *Service) GetUpdate(":         chanRead,
		"func (s *Service) ListAdmins(":        chanRead,
		"func (s *Service) Report(":            "s.authorize(ctx, channelID, &reporterID, accessReport)",
	} {
		body, ok := funcBody(src, sig)
		if !ok {
			t.Errorf("%s not found", sig)
			continue
		}
		g := strings.Index(body, gate)
		if g < 0 {
			t.Errorf("%s does not run the gate %q", sig, gate)
			continue
		}
		if st := strings.Index(body, "s.store."); st >= 0 && st < g {
			t.Errorf("%s reads the store before the gate", sig)
		}
	}
}

// CH-M2 wiring: comment delete and pin take their authority from the gated
// role and hand the PATH update to the store, which matches on it.
func TestCommentModerationIsScopedToThePathUpdate(t *testing.T) {
	src := readSources(t, "channel.go")
	for sig, want := range map[string][]string{
		"func (s *Service) DeleteComment(": {
			`moderator := isAtLeast(ga.role, "moderator")`,
			"s.store.DeleteComment(ctx, commentID, updateID, userID, moderator)",
		},
		"func (s *Service) PinComment(": {
			`if !isAtLeast(ga.role, "admin") {`,
			"s.store.PinComment(ctx, commentID, updateID)",
		},
	} {
		body, ok := funcBody(src, sig)
		if !ok {
			t.Fatalf("%s not found", sig)
		}
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Errorf("%s: missing %q", sig, w)
			}
		}
	}
}

// CH-M3 wiring: Unsubscribe and RemoveMember never delete a banned row.
func TestLeavingAndRemovalKeepTheBan(t *testing.T) {
	src := readSources(t, "channel.go", "moderation.go")
	for sig, want := range map[string]string{
		"func (s *Service) Unsubscribe(":  "remove, err := guardLeave(member)\n\tif err != nil || !remove {\n\t\treturn err\n\t}",
		"func (s *Service) RemoveMember(": "if target.Role == \"banned\" {\n\t\treturn ErrMemberIsBanned\n\t}",
	} {
		body, ok := funcBody(src, sig)
		if !ok {
			t.Fatalf("%s not found", sig)
		}
		g, d := strings.Index(body, want), strings.Index(body, "s.store.RemoveMember(")
		if g < 0 {
			t.Errorf("%s does not keep the ban (%q)", sig, want)
			continue
		}
		if d < 0 || d < g {
			t.Errorf("%s deletes before checking for a ban", sig)
		}
	}
}

// CH-L3 wiring: AddAdmin never inserts a member row.
func TestAddAdminOnlyPromotesExistingMembers(t *testing.T) {
	body, ok := funcBody(readSources(t, "community.go"), "func (s *Service) AddAdmin(")
	if !ok {
		t.Fatal("AddAdmin not found")
	}
	if strings.Contains(body, "AddMember(") {
		t.Fatal("AddAdmin must not insert a member: that skipped the join gates")
	}
	if !strings.Contains(body, "promote, err := guardPromote(member)\n\tif err != nil || !promote {\n\t\treturn err\n\t}") {
		t.Fatal("AddAdmin must apply guardPromote before promoting")
	}
}

func readSources(t *testing.T, files ...string) string {
	t.Helper()
	var b strings.Builder
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		// A Windows checkout has CRLF; the pinned snippets are LF.
		b.WriteString(strings.ReplaceAll(string(raw), "\r\n", "\n"))
		b.WriteString("\n")
	}
	return b.String()
}

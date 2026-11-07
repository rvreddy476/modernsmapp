// DB-backed proof of the 2026-11-07 audit fixes (channel-service: CH-M1,
// CH-M2, CH-M3, CH-L4). The unit tests pin the decisions and the SQL; these
// run the real statements.
//
// Requires TEST_PG_DSN on a "_test" database (channel_it_test); skipped
// when unset, like the other suites in this package. Every test seeds its
// own channels and users.
package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atpost/channel-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedGateChannel makes an active channel of the given type with comments
// enabled, and its owner's member row.
func seedGateChannel(t *testing.T, pool *pgxpool.Pool, channelType string, owner uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var id uuid.UUID
	handle := "ag" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	if err := pool.QueryRow(ctx, `
		INSERT INTO broadcast_channels (owner_id, handle, name, channel_type, status, comment_mode)
		VALUES ($1, $2, 'audit gate channel', $3, 'active', 'enabled') RETURNING id`,
		owner, handle, channelType).Scan(&id); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	seedGateMember(t, pool, id, owner, "owner")
	return id
}

func seedGateMember(t *testing.T, pool *pgxpool.Pool, channelID, userID uuid.UUID, role string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO channel_members (channel_id, user_id, role) VALUES ($1, $2, $3)
		ON CONFLICT (channel_id, user_id) DO UPDATE SET role = $3`,
		channelID, userID, role); err != nil {
		t.Fatalf("seed member: %v", err)
	}
}

func seedGateUpdate(t *testing.T, pool *pgxpool.Pool, channelID uuid.UUID, updateType string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO channel_updates (channel_id, author_id, update_type, body, status, published_at)
		VALUES ($1, $2, $3, 'update', 'published', $4) RETURNING id`,
		channelID, uuid.New(), updateType, time.Now().Add(-time.Minute)).Scan(&id); err != nil {
		t.Fatalf("seed update: %v", err)
	}
	return id
}

func commentExists(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) (exists, pinned bool) {
	t.Helper()
	err := pool.QueryRow(context.Background(),
		`SELECT is_pinned FROM update_comments WHERE id = $1`, id).Scan(&pinned)
	if err != nil {
		return false, false
	}
	return true, pinned
}

// CH-M2: the owner of channel A cannot delete or pin a comment that lives
// on channel B, whichever update id they put in the path.
func TestAuditIT_CommentModerationIsScopedToTheChannel(t *testing.T) {
	svc, _, pool := setupReactionCountIT(t)
	ctx := context.Background()
	ownerA, ownerB, commenter := uuid.New(), uuid.New(), uuid.New()
	chA := seedGateChannel(t, pool, "public", ownerA)
	chB := seedGateChannel(t, pool, "public", ownerB)
	upA := seedGateUpdate(t, pool, chA, "announcement")
	upB := seedGateUpdate(t, pool, chB, "announcement")

	victim, err := svc.AddComment(ctx, chB, upB, commenter, "on B", nil)
	if err != nil {
		t.Fatalf("comment on B: %v", err)
	}

	if err := svc.DeleteComment(ctx, chA, upA, victim.ID, ownerA); err == nil {
		t.Fatal("A's owner deleted B's comment through A's own update")
	}
	if err := svc.DeleteComment(ctx, chA, upB, victim.ID, ownerA); err == nil {
		t.Fatal("A's owner deleted B's comment by naming B's update under A")
	}
	if err := svc.PinComment(ctx, chA, upA, victim.ID, ownerA); err == nil {
		t.Fatal("A's owner pinned B's comment")
	}
	if exists, pinned := commentExists(t, pool, victim.ID); !exists || pinned {
		t.Fatalf("B's comment after the cross-channel attempts: exists=%v pinned=%v, want true/false", exists, pinned)
	}

	// A reply cannot hang off another update's comment.
	if _, err := svc.AddComment(ctx, chA, upA, commenter, "reply", &victim.ID); err == nil {
		t.Fatal("a reply on A attached to a comment on B")
	}

	// B's own owner still moderates B.
	if err := svc.PinComment(ctx, chB, upB, victim.ID, ownerB); err != nil {
		t.Fatalf("B's owner pinning on B: %v", err)
	}
	if err := svc.DeleteComment(ctx, chB, upB, victim.ID, ownerB); err != nil {
		t.Fatalf("B's owner deleting on B: %v", err)
	}
	if exists, _ := commentExists(t, pool, victim.ID); exists {
		t.Fatal("B's owner could not delete a comment on B")
	}
}

// CH-M1: the comment, delta, poll, RSVP and attendee routes refuse a banned
// member and an outsider of a private channel, and the delta no longer
// reads an update under someone else's channel id.
func TestAuditIT_ChildRoutesRunTheGate(t *testing.T) {
	svc, _, pool := setupReactionCountIT(t)
	ctx := context.Background()
	owner, banned, outsider := uuid.New(), uuid.New(), uuid.New()
	public := seedGateChannel(t, pool, "public", owner)
	private := seedGateChannel(t, pool, "private", owner)
	seedGateMember(t, pool, public, banned, "banned")
	pubEvent := seedGateUpdate(t, pool, public, "event")
	pubPoll := seedGateUpdate(t, pool, public, "poll")
	privUpdate := seedGateUpdate(t, pool, private, "event")
	since := time.Now().Add(-time.Hour)

	// Banned on a public community: every child route refuses.
	if _, err := svc.AddComment(ctx, public, pubEvent, banned, "hi", nil); !errors.Is(err, ErrChannelMemberBanned) {
		t.Errorf("banned AddComment = %v, want ErrChannelMemberBanned", err)
	}
	if _, err := svc.ListComments(ctx, public, pubEvent, &banned, "newest", 20, 0); !errors.Is(err, ErrChannelMemberBanned) {
		t.Errorf("banned ListComments = %v, want ErrChannelMemberBanned", err)
	}
	if err := svc.RSVPEvent(ctx, public, pubEvent, banned, "going"); !errors.Is(err, ErrChannelMemberBanned) {
		t.Errorf("banned RSVPEvent = %v, want ErrChannelMemberBanned", err)
	}
	if err := svc.VoteOnPoll(ctx, public, pubPoll, banned, []int{0}); !errors.Is(err, ErrChannelMemberBanned) {
		t.Errorf("banned VoteOnPoll = %v, want ErrChannelMemberBanned", err)
	}
	if _, err := svc.GetPollResults(ctx, public, pubPoll, &banned); !errors.Is(err, ErrChannelMemberBanned) {
		t.Errorf("banned GetPollResults = %v, want ErrChannelMemberBanned", err)
	}
	if _, err := svc.ListAttendees(ctx, public, pubEvent, &banned, "", 20, 0); !errors.Is(err, ErrChannelMemberBanned) {
		t.Errorf("banned ListAttendees = %v, want ErrChannelMemberBanned", err)
	}

	// An outsider of a private community: hidden.
	if _, err := svc.ListComments(ctx, private, privUpdate, &outsider, "newest", 20, 0); !errors.Is(err, ErrChannelNotFound) {
		t.Errorf("outsider ListComments on private = %v, want ErrChannelNotFound", err)
	}
	if _, err := svc.ListAttendees(ctx, private, privUpdate, nil, "", 20, 0); !errors.Is(err, ErrChannelNotFound) {
		t.Errorf("anonymous ListAttendees on private = %v, want ErrChannelNotFound", err)
	}
	// The delta under a public channel id with the private update's id.
	if _, err := svc.ListCommentsSince(ctx, public, privUpdate, &outsider, since, 50); !errors.Is(err, ErrUpdateNotFound) {
		t.Errorf("delta for a private update under a public channel id = %v, want ErrUpdateNotFound", err)
	}

	// A plain viewer of the public community is unaffected.
	if err := svc.RSVPEvent(ctx, public, pubEvent, outsider, "going"); err != nil {
		t.Errorf("public RSVP: %v", err)
	}
	if _, err := svc.ListCommentsSince(ctx, public, pubEvent, nil, since, 50); err != nil {
		t.Errorf("public delta: %v", err)
	}
}

// CH-M3: leaving does not erase a ban, and the ban still refuses a rejoin.
func TestAuditIT_BanSurvivesUnsubscribe(t *testing.T) {
	svc, st, pool := setupReactionCountIT(t)
	ctx := context.Background()
	owner, banned := uuid.New(), uuid.New()
	ch := seedGateChannel(t, pool, "public", owner)
	seedGateMember(t, pool, ch, banned, "banned")

	if err := svc.Unsubscribe(ctx, ch, banned); err != nil {
		t.Fatalf("banned unsubscribe: %v", err)
	}
	if role := st.GetMemberRole(ctx, ch, banned); role != "banned" {
		t.Fatalf("after unsubscribe the role is %q, want banned: leaving erased the ban", role)
	}
	if err := svc.Subscribe(ctx, ch, banned); err == nil {
		t.Fatal("a banned user rejoined after unsubscribing")
	}
	if _, err := st.AddMember(ctx, &store.ChannelMember{ChannelID: ch, UserID: banned, Role: "subscriber", NotifyOn: "all"}); !errors.Is(err, store.ErrMemberBanned) {
		t.Fatalf("AddMember over a ban = %v, want store.ErrMemberBanned", err)
	}
	if err := svc.RemoveMember(ctx, ch, owner, banned); !errors.Is(err, ErrMemberIsBanned) {
		t.Fatalf("removing a banned member = %v, want ErrMemberIsBanned", err)
	}
	if role := st.GetMemberRole(ctx, ch, banned); role != "banned" {
		t.Fatalf("after the refused removal the role is %q, want banned", role)
	}
}

// CH-L4: /my is one ordered page, never longer than the limit, with no
// repeats across pages and no community the user is banned from.
func TestAuditIT_MyChannelsPagesOnceWithoutBans(t *testing.T) {
	svc, _, pool := setupReactionCountIT(t)
	ctx := context.Background()
	me := uuid.New()
	want := map[uuid.UUID]bool{}
	for i := 0; i < 2; i++ {
		want[seedGateChannel(t, pool, "private", me)] = true
	}
	for i := 0; i < 2; i++ {
		ch := seedGateChannel(t, pool, "public", uuid.New())
		seedGateMember(t, pool, ch, me, "subscriber")
		want[ch] = true
	}
	bannedFrom := seedGateChannel(t, pool, "public", uuid.New())
	seedGateMember(t, pool, bannedFrom, me, "banned")

	first, err := svc.GetMyChannels(ctx, me, 3, 0)
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	second, err := svc.GetMyChannels(ctx, me, 3, 3)
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(first) != 3 || len(second) != 1 {
		t.Fatalf("pages of %d and %d, want 3 and 1", len(first), len(second))
	}
	seen := map[uuid.UUID]bool{}
	for _, c := range append(first, second...) {
		if c.ID == bannedFrom {
			t.Fatal("/my lists a community the user is banned from")
		}
		if seen[c.ID] {
			t.Fatalf("channel %s listed twice across pages", c.ID)
		}
		seen[c.ID] = true
		if !want[c.ID] {
			t.Fatalf("unexpected channel %s", c.ID)
		}
	}
	if len(seen) != len(want) {
		t.Fatalf("listed %d channels, want %d", len(seen), len(want))
	}
}

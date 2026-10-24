//go:build integration

package postgres

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Hidden chats against a real PostgreSQL. CHAT_POSTGRES_DSN must name a
// database whose name ends in _test (refused otherwise).
func hiddenTestStore(t *testing.T) (*ConversationStore, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("CHAT_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("CHAT_POSTGRES_DSN is required (a *_test database)")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatalf("refusing database %q: name must end in _test", cfg.ConnConfig.Database)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := BootstrapSchema(context.Background(), pool, database.SetupSQL); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	return New(pool), pool
}

func seedHiddenGroup(t *testing.T, pool *pgxpool.Pool, members ...uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	conv := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO chat.conversations (id, type, title, created_by, last_message_at)
		VALUES ($1, 'group', 'hidden chats', $2, NOW())`, conv, members[0]); err != nil {
		t.Fatal(err)
	}
	for i, m := range members {
		role := "member"
		if i == 0 {
			role = "owner"
		}
		if _, err := pool.Exec(ctx, `INSERT INTO chat.conversation_members (conversation_id, user_id, role, join_gen)
			VALUES ($1, $2, $3, nextval('chat.membership_gen_seq'))`, conv, m, role); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM chat.revocation_intents WHERE conversation_id = $1`, conv)
		_, _ = pool.Exec(ctx, `DELETE FROM chat.conversation_members WHERE conversation_id = $1`, conv)
		_, _ = pool.Exec(ctx, `DELETE FROM chat.conversations WHERE id = $1`, conv)
	})
	return conv
}

func TestHiddenChatLockFiltersEveryListForTheHiderOnly(t *testing.T) {
	s, pool := hiddenTestStore(t)
	ctx := context.Background()
	hider, other := uuid.New(), uuid.New()
	conv := seedHiddenGroup(t, pool, hider, other)

	changed, oldGen, err := s.LockConversationForMember(ctx, conv, hider)
	if err != nil || !changed || oldGen == 0 {
		t.Fatalf("lock: changed=%v gen=%d err=%v", changed, oldGen, err)
	}
	if again, _, _ := s.LockConversationForMember(ctx, conv, hider); again {
		t.Fatal("second lock reported a change")
	}
	var intent int64
	if err := pool.QueryRow(ctx, `SELECT sever_gen FROM chat.revocation_intents WHERE conversation_id = $1 AND user_id = $2`, conv, hider).Scan(&intent); err != nil || intent != oldGen {
		t.Fatalf("revocation intent for the old generation: %d %v", intent, err)
	}

	for _, lane := range []string{LanePrimary, LaneArchived, LaneRequests} {
		rows, err := s.ListInboxConversations(ctx, hider, lane, false, 50, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range rows {
			if c.ID == conv {
				t.Fatalf("lane %s lists the hidden chat for the hider", lane)
			}
		}
	}
	legacy, _ := s.ListConversationsByUser(ctx, hider, 50, nil, nil)
	for _, c := range legacy {
		if c.ID == conv {
			t.Fatal("legacy list shows the hidden chat")
		}
	}
	if found, _ := s.SearchMyConversations(ctx, hider, "hidden", 20); len(found) != 0 {
		t.Fatal("conversation search finds the hidden chat")
	}
	if st, _ := s.GetMemberSearchStates(ctx, hider, []uuid.UUID{conv}); len(st) != 0 {
		t.Fatal("message search keeps hits from the hidden chat")
	}
	// The other member is unaffected.
	otherRows, _ := s.ListInboxConversations(ctx, other, LanePrimary, false, 50, nil, nil)
	seen := false
	for _, c := range otherRows {
		seen = seen || c.ID == conv
	}
	if !seen {
		t.Fatal("hiding removed the chat from the OTHER member's inbox")
	}
	// The hidden list has it.
	hidden, err := s.ListHiddenConversations(ctx, hider, 20, nil, nil)
	if err != nil || len(hidden) != 1 || hidden[0].ID != conv {
		t.Fatalf("hidden list: %v %v", hidden, err)
	}
	members, _ := s.GetMembers(ctx, conv)
	for _, m := range members {
		if (m.UserID == hider) != m.Locked {
			t.Fatalf("member lock flags wrong: %+v", members)
		}
	}
	if err := s.UnlockConversationForMember(ctx, conv, hider); err != nil {
		t.Fatal(err)
	}
	if l, _ := s.IsConversationLockedFor(ctx, conv, hider); l {
		t.Fatal("unhide did not unlock")
	}
}

func TestHiddenChatLockoutSurvivesANewStore(t *testing.T) {
	s, pool := hiddenTestStore(t)
	ctx := context.Background()
	u := uuid.New()
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM chat.hidden_chat_attempts WHERE user_id = $1`, u) })
	now := time.Now().UTC()
	ladder := []time.Duration{15 * time.Minute, time.Hour, 24 * time.Hour}
	for i := 0; i < 5; i++ {
		if _, err := s.RecordHiddenChatFailure(ctx, u, SecretKindKey, now, 5, ladder); err != nil {
			t.Fatal(err)
		}
	}
	restarted := New(pool) // a fresh process
	st, err := restarted.GetHiddenChatAttempts(ctx, u, SecretKindKey)
	if err != nil || st.LockedUntil == nil || st.LockedUntil.Before(now.Add(14*time.Minute)) || st.LockoutLevel != 1 {
		t.Fatalf("lockout lost across a restart: %+v %v", st, err)
	}
}

func TestHiddenChatStrictResetWipesOnlyThatUser(t *testing.T) {
	s, pool := hiddenTestStore(t)
	ctx := context.Background()
	u, peer := uuid.New(), uuid.New()
	mine := seedHiddenGroup(t, pool, u, peer)
	theirs := seedHiddenGroup(t, pool, peer, u)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM chat.hidden_chat_settings WHERE user_id = $1`, u)
	})
	_, _, _ = s.LockConversationForMember(ctx, mine, u)
	_, _, _ = s.LockConversationForMember(ctx, theirs, peer)
	wiped, err := s.ResetHiddenChats(ctx, HiddenChatsReset{
		Settings: HiddenChatSettings{UserID: u, SecretKeyHash: "$argon2id$v=19$a", ChatPasswordHash: "$argon2id$v=19$b"},
		Wipe:     true, At: time.Now().UTC(),
	})
	if err != nil || len(wiped) != 1 || wiped[0] != mine {
		t.Fatalf("wiped = %v err = %v", wiped, err)
	}
	var hidden, locked bool
	if err := pool.QueryRow(ctx, `SELECT hidden, locked FROM chat.conversation_members WHERE conversation_id = $1 AND user_id = $2`, mine, u).Scan(&hidden, &locked); err != nil || !hidden || locked {
		t.Fatalf("wiped row: hidden=%v locked=%v err=%v", hidden, locked, err)
	}
	if l, _ := s.IsConversationLockedFor(ctx, theirs, peer); !l {
		t.Fatal("strict reset touched another user's hidden chat")
	}
	var peerHidden bool
	_ = pool.QueryRow(ctx, `SELECT hidden FROM chat.conversation_members WHERE conversation_id = $1 AND user_id = $2`, mine, peer).Scan(&peerHidden)
	if peerHidden {
		t.Fatal("strict reset cleared the OTHER member's copy")
	}
	// The CHECK refuses a clear secret.
	if err := s.SaveHiddenChatSettings(ctx, HiddenChatSettings{UserID: u, SecretKeyHash: "Mango42", ChatPasswordHash: "$argon2id$x"}); err == nil {
		t.Fatal("the database accepted a clear secret")
	}
}

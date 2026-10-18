//go:build integration

package service

// Live-PostgreSQL proof of the call mute (chat parity §4): a muted callee is
// invited (so the call logs as missed for them) but never rung — no
// CallInvited outbox row (the push) and no ringing pair authorization (the
// gate ws-gateway checks before relaying call_ring / call_offer).
//
// DISPOSABLE databases only (the test drops schema calls):
//
//	CALL_MUTE_TEST_DSN=postgres://postgres:postgres@localhost:5432/call_b2_mute_test?sslmode=disable \
//	CALL_MUTE_TEST_REDIS=localhost:6379 CALL_MUTE_TEST_REDIS_DB=13 \
//	  go test -tags integration -run CallMutePG ./internal/service/ -count=1

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/atpost/chat-call-service/database"
	"github.com/atpost/chat-call-service/internal/domain"
	"github.com/atpost/chat-call-service/internal/sfu"
	store "github.com/atpost/chat-call-service/internal/store/postgres"
	"github.com/atpost/chat-shared/callauth"
	events "github.com/atpost/chat-shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type staticMute map[uuid.UUID]bool

func (m staticMute) CallsMuted(_ context.Context, callee, _ uuid.UUID, _ *uuid.UUID) bool {
	return m[callee]
}

func TestCallMutePGMutedCalleeIsInvitedButNotRung(t *testing.T) {
	dsn := os.Getenv("CALL_MUTE_TEST_DSN")
	if dsn == "" {
		t.Skip("CALL_MUTE_TEST_DSN not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var dbName string
	if err := pool.QueryRow(ctx, `SELECT current_database()`).Scan(&dbName); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(dbName, "_test") {
		t.Fatalf("refusing to run against %q: database name must end in _test", dbName)
	}
	if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS calls CASCADE`); err != nil {
		t.Fatal(err)
	}
	if err := store.BootstrapSchema(ctx, pool, database.SetupSQL); err != nil {
		t.Fatal(err)
	}
	redisDB, _ := strconv.Atoi(os.Getenv("CALL_MUTE_TEST_REDIS_DB"))
	if redisDB == 0 {
		t.Fatal("CALL_MUTE_TEST_REDIS_DB must name a non-zero scratch Redis DB (db 0 is dev)")
	}
	addr := os.Getenv("CALL_MUTE_TEST_REDIS")
	if addr == "" {
		addr = "localhost:6379"
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr, DB: redisDB})
	defer rdb.Close()

	caller, muted, rung := uuid.New(), uuid.New(), uuid.New()
	svc := New(store.NewCallStore(pool), sfu.NewStubProvider(), NewRateLimiter(rdb), nil, rdb,
		slog.New(slog.NewTextHandler(io.Discard, nil)), 30).
		WithCallMuteChecker(staticMute{muted: true})

	place := func(target uuid.UUID) uuid.UUID {
		t.Helper()
		created, err := svc.CreateCall(ctx, caller, CreateCallRequest{
			CallType: domain.CallTypeDirectAudio, SourceType: domain.SourceTypeChat,
			TargetUserIDs: []uuid.UUID{target}, AudioOnly: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		return created.ID
	}
	invitedEvents := func(target uuid.UUID) int {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM calls.outbox_events
			WHERE event_type = $1 AND payload->>'invitee_user_id' = $2`, events.CallInvited, target.String()).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	mutedCall := place(muted)
	if invitedEvents(muted) != 0 {
		t.Fatal("muted callee got a CallInvited push event")
	}
	if st, err := callauth.Get(ctx, rdb, caller, muted); err != nil || st != nil {
		t.Fatalf("muted pair must hold no ringing authorization (no ring frame): %+v %v", st, err)
	}
	if inv, err := svc.store.GetInviteByCallAndUser(ctx, mutedCall, muted); err != nil || inv == nil {
		t.Fatalf("muted callee must still be invited so the call logs as missed: %v", err)
	}
	// Ring timeout: the invite expires — the missed-call record for them.
	svc.endCallInternal(ctx, mutedCall, caller, domain.EndedReasonMissed)
	if inv, _ := svc.store.GetInviteByCallAndUser(ctx, mutedCall, muted); inv == nil || inv.ResponseStatus != domain.ResponseStatusExpired {
		t.Fatalf("muted callee's invite must end expired (missed), got %+v", inv)
	}

	// Control: an unmuted callee rings exactly as before.
	place(rung)
	if invitedEvents(rung) != 1 {
		t.Fatal("unmuted callee must get the CallInvited push event")
	}
	if st, err := callauth.Get(ctx, rdb, caller, rung); err != nil || st == nil || st.State != callauth.StateRinging {
		t.Fatalf("unmuted pair must be authorized ringing: %+v %v", st, err)
	}
	_ = rdb.Del(ctx, callauth.PairKey(caller, rung), callauth.PairKey(caller, muted)).Err()
}

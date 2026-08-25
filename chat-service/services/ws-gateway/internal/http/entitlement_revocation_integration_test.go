//go:build integration

package http

// Re-verification P0-4 proofs against real Redis:
//
//  1. REPLAY: a removed member's still-valid token must not restore the room
//     feed once the sever-generation marker is armed.
//  2. REJOIN: a legitimately rejoined member's fresh token (newer membership
//     generation) must be admitted WITHOUT any marker clearing — the DEL
//     that used to admit them was the clear/remove race.
//  3. LOST FRAME: if the eager subscription_revoked publish is lost, the
//     gateway's periodic reconciliation must still drop the live
//     subscription once the marker exists.
//
// Removing the gateway's deny-generation check fails 1 and 2's negative
// arm; removing the reconcile loop fails 3.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atpost/chat-shared/roomauth"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

type entitlementHarness struct {
	t          *testing.T
	rdb        *redis.Client
	connection *websocket.Conn
	userID     uuid.UUID
	convID     uuid.UUID
	secret     string
}

func newEntitlementHarness(t *testing.T, reconcile time.Duration) *entitlementHarness {
	t.Helper()
	redisAddress := os.Getenv("REDIS_ADDR")
	if redisAddress == "" {
		t.Fatal("REDIS_ADDR is required")
	}
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: redisAddress})
	t.Cleanup(func() {
		time.Sleep(100 * time.Millisecond)
		rdb.Close()
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}

	userID, convID := uuid.New(), uuid.New()
	jwtSecret := "p04-replay-jwt-secret"
	entitlementSecret := "p04-replay-entitlement-secret"
	server := httptest.NewServer(NewServer(rdb, nil, ServerOptions{
		JWTSecret:                     jwtSecret,
		EnableScopedRooms:             false,
		EntitlementSecret:             entitlementSecret,
		SubscriptionReconcileInterval: reconcile,
	}).Routes())
	t.Cleanup(server.Close)

	token := signJWT(t, map[string]any{"alg": "HS256"}, map[string]any{
		"sub": userID.String(), "exp": time.Now().Add(time.Hour).Unix(),
	}, jwtSecret)
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)
	connection, _, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(server.URL, "http")+"/v1/ws/connect", header)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	time.Sleep(200 * time.Millisecond) // personal-channel subscription settles

	return &entitlementHarness{
		t: t, rdb: rdb, connection: connection,
		userID: userID, convID: convID, secret: entitlementSecret,
	}
}

func (h *entitlementHarness) token(gen int64) string {
	h.t.Helper()
	token, err := roomauth.Sign(roomauth.Claims{
		Version:        1,
		Subject:        h.userID.String(),
		ConversationID: h.convID.String(),
		Audience:       roomauth.Audience,
		ExpiresAt:      time.Now().Add(roomauth.TTL).Unix(),
		Nonce:          "p04-nonce",
		Gen:            gen,
	}, h.secret)
	if err != nil {
		h.t.Fatal(err)
	}
	return token
}

func (h *entitlementHarness) subscribe(entitlement string) {
	h.t.Helper()
	frame, _ := json.Marshal(map[string]any{
		"type": "conversation.subscribe", "entitlement": entitlement,
	})
	if err := h.connection.WriteMessage(websocket.TextMessage, frame); err != nil {
		h.t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
}

// armMarker writes the sever-generation marker exactly as armRevocation does.
func (h *entitlementHarness) armMarker(severGen int64) {
	h.t.Helper()
	err := h.rdb.Set(context.Background(),
		roomauth.DenyKey(h.convID.String(), h.userID.String()),
		strconv.FormatInt(severGen, 10), roomauth.DenyTTL).Err()
	if err != nil {
		h.t.Fatal(err)
	}
}

func (h *entitlementHarness) publishRevokedFrame() {
	h.t.Helper()
	revoked, _ := json.Marshal(map[string]any{
		"type": "subscription_revoked", "conversation_id": h.convID.String(),
	})
	if err := h.rdb.Publish(context.Background(), "chat:"+h.userID.String(), revoked).Err(); err != nil {
		h.t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
}

func (h *entitlementHarness) expectRoomDelivery(marker string, want bool) {
	h.t.Helper()
	channel := "convroom:" + h.convID.String()
	if err := h.rdb.Publish(context.Background(), channel,
		`{"type":"message.new","probe":"`+marker+`"}`).Err(); err != nil {
		h.t.Fatal(err)
	}
	deadline := time.Now().Add(1 * time.Second)
	got := false
	for time.Now().Before(deadline) {
		_ = h.connection.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		_, raw, err := h.connection.ReadMessage()
		if err != nil {
			break
		}
		if strings.Contains(string(raw), marker) {
			got = true
			break
		}
	}
	if got != want {
		h.t.Fatalf("room delivery for %s: got %v want %v", marker, got, want)
	}
}

func TestRevokedEntitlementCannotBeReplayed(t *testing.T) {
	h := newEntitlementHarness(t, time.Hour) // reconciliation out of the way
	joinedGen := time.Now().Add(-time.Hour).UnixNano()

	// 1. A live member's entitled subscribe delivers the room feed.
	h.subscribe(h.token(joinedGen))
	h.expectRoomDelivery("before-removal", true)

	// 2. Removal: marker (sever generation NOW > joinedGen) + eager frame.
	h.armMarker(time.Now().UnixNano())
	h.publishRevokedFrame()

	// 3. Replaying the STILL-VALID pre-removal token must not restore it.
	h.subscribe(h.token(joinedGen))
	h.expectRoomDelivery("after-replay", false)
}

func TestRejoinedMemberAdmittedWithoutMarkerClear(t *testing.T) {
	h := newEntitlementHarness(t, time.Hour)

	// Removal armed the marker; the marker is NEVER cleared. (That an
	// old-generation token stays dead under this marker is what
	// TestRevokedEntitlementCannotBeReplayed proves; a websocket read
	// timeout poisons the test connection, so this test asserts only the
	// admit side on a fresh connection.)
	severGen := time.Now().UnixNano()
	h.armMarker(severGen)

	// The rejoined member's fresh token (newer joined_at) is admitted with
	// the marker still present — the design that removes the clear/remove
	// race entirely.
	h.subscribe(h.token(severGen + int64(time.Second)))
	h.expectRoomDelivery("new-gen", true)
}

func TestReconciliationDropsSubscriptionWhenFrameIsLost(t *testing.T) {
	h := newEntitlementHarness(t, 300*time.Millisecond)
	joinedGen := time.Now().Add(-time.Hour).UnixNano()

	h.subscribe(h.token(joinedGen))
	h.expectRoomDelivery("before-lost-frame", true)

	// Removal whose subscription_revoked publish is LOST: only the marker
	// lands. The periodic reconciliation must still evict the live
	// subscription.
	h.armMarker(time.Now().UnixNano())
	time.Sleep(900 * time.Millisecond) // > two reconcile sweeps

	h.expectRoomDelivery("after-lost-frame", false)
}

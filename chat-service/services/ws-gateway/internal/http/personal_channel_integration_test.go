//go:build integration

package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

func TestBetaSocketReceivesOnlyItsPersonalChannel(t *testing.T) {
	redisAddress := os.Getenv("REDIS_ADDR")
	if redisAddress == "" {
		t.Fatal("REDIS_ADDR is required")
	}
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: redisAddress})
	defer func() {
		time.Sleep(100 * time.Millisecond)
		rdb.Close()
	}()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}

	userID, otherID, postID := uuid.New(), uuid.New(), uuid.New()
	secret := "m5-ws-integration-secret"
	server := httptest.NewServer(NewServer(rdb, nil, ServerOptions{JWTSecret: secret, EnableScopedRooms: false}).Routes())
	defer server.Close()
	token := signJWT(t, map[string]any{"alg": "HS256"}, map[string]any{
		"sub": userID.String(), "exp": time.Now().Add(time.Hour).Unix(),
	}, secret)
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)
	connection, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/v1/ws/connect", header)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	// A client-selected room request is rejected before the switch can call
	// Redis Subscribe. Global and another user's channels were never part of
	// the initial subscription either.
	subscribeFrame, _ := json.Marshal(map[string]any{"type": "subscribe_post", "post_id": postID.String()})
	if err := connection.WriteMessage(websocket.TextMessage, subscribeFrame); err != nil {
		t.Fatal(err)
	}
	for channel, payload := range map[string]string{
		"feed:new_post":            "GLOBAL-LEAK",
		"presence:updates":         "PRESENCE-LEAK",
		"chat:" + otherID.String(): "OTHER-USER-LEAK",
		"post:" + postID.String():  "CLIENT-ROOM-LEAK",
	} {
		if err := rdb.Publish(ctx, channel, payload).Err(); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(200 * time.Millisecond)
	const personalPayload = `{"type":"message","payload":{"text":"PERSONAL-ONLY"}}`
	if err := rdb.Publish(ctx, "chat:"+userID.String(), personalPayload).Err(); err != nil {
		t.Fatal(err)
	}
	if err := connection.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, received, err := connection.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if string(received) != personalPayload {
		t.Fatalf("beta socket received non-personal data: %q", received)
	}

	// Direct signaling is also disabled in beta and must create no publish to
	// the target user's personal channel.
	targetSubscription := rdb.Subscribe(ctx, "chat:"+otherID.String())
	defer targetSubscription.Close()
	if _, err := targetSubscription.Receive(ctx); err != nil {
		t.Fatal(err)
	}
	offer, _ := json.Marshal(map[string]any{
		"type": "call_offer", "target_user_id": otherID.String(), "call_id": uuid.NewString(),
	})
	if err := connection.WriteMessage(websocket.TextMessage, offer); err != nil {
		t.Fatal(err)
	}
	select {
	case leaked := <-targetSubscription.Channel():
		t.Fatalf("disabled direct signaling published to another user: %q", leaked.Payload)
	case <-time.After(500 * time.Millisecond):
	}
}

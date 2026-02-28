package ws

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

var upgrader = websocket.Upgrader{
	CheckOrigin:  func(r *http.Request) bool { return true },
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

// Hub manages all active WebSocket connections and message routing.
type Hub struct {
	rdb       *redis.Client
	jwtSecret []byte
	mu        sync.RWMutex
	conns     map[string]*conn
}

type conn struct {
	ws     *websocket.Conn
	userID string
	cancel context.CancelFunc
	send   chan []byte
}

func NewHub(rdb *redis.Client, jwtSecret string) *Hub {
	return &Hub{
		rdb:       rdb,
		jwtSecret: []byte(jwtSecret),
		conns:     make(map[string]*conn),
	}
}

// HandleConnect is the Gin handler for GET /v1/ws/connect?access_token=<jwt>
func (h *Hub) HandleConnect(c *gin.Context) {
	token := c.Query("access_token")
	if token == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing access_token"})
		return
	}

	userID, err := h.verifyJWT(token)
	if err != nil {
		slog.Warn("ws auth failed", "error", err)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
		return
	}

	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		slog.Error("websocket upgrade failed", "error", err)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	cn := &conn{ws: ws, userID: userID, cancel: cancel, send: make(chan []byte, 64)}

	// Evict previous connection for same user
	h.mu.Lock()
	if old, ok := h.conns[userID]; ok {
		old.cancel()
		old.ws.Close()
	}
	h.conns[userID] = cn
	h.mu.Unlock()

	slog.Info("ws connected", "user_id", userID)

	go h.writePump(ctx, cn)
	go h.readPump(cn)
}

// writePump subscribes to Redis PubSub and writes messages + pings to the client.
func (h *Hub) writePump(ctx context.Context, c *conn) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	channel := "chat:" + c.userID
	sub := h.rdb.Subscribe(ctx, channel)
	defer sub.Close()

	redisCh := sub.Channel()

	defer func() {
		c.cancel()
		c.ws.Close()
	}()

	for {
		select {
		case <-ctx.Done():
			return

		case msg, ok := <-redisCh:
			if !ok {
				return
			}
			payload := normalizePayload(msg.Payload)
			c.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.ws.WriteMessage(websocket.TextMessage, payload); err != nil {
				return
			}

		case data, ok := <-c.send:
			if !ok {
				return
			}
			c.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.ws.WriteMessage(websocket.TextMessage, data); err != nil {
				return
			}

		case <-ticker.C:
			c.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// readPump reads client messages (call signaling) and forwards them.
func (h *Hub) readPump(c *conn) {
	defer func() {
		h.mu.Lock()
		if h.conns[c.userID] == c {
			delete(h.conns, c.userID)
		}
		h.mu.Unlock()
		c.cancel()
		c.ws.Close()
		slog.Info("ws disconnected", "user_id", c.userID)
	}()

	c.ws.SetReadLimit(4096)
	c.ws.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.ws.SetPongHandler(func(string) error {
		c.ws.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, raw, err := c.ws.ReadMessage()
		if err != nil {
			return
		}

		var data map[string]interface{}
		if err := json.Unmarshal(raw, &data); err != nil {
			continue
		}

		// Forward signaling to target user (call_offer, call_answer, ice_candidate, etc.)
		targetUserID, _ := data["target_user_id"].(string)
		if targetUserID == "" {
			continue
		}

		data["sender_id"] = c.userID
		forwarded, _ := json.Marshal(data)

		h.mu.RLock()
		target, ok := h.conns[targetUserID]
		h.mu.RUnlock()

		if ok {
			select {
			case target.send <- forwarded:
			default:
				// drop if buffer full
			}
		}
	}
}

// normalizePayload converts Redis PubSub payload to the format the frontend expects.
// The service publishes {"type":"new_message","payload":{...}} but the frontend
// expects {"type":"message","payload":{...}}.
func normalizePayload(raw string) []byte {
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return []byte(raw)
	}
	if t, _ := data["type"].(string); t == "new_message" {
		data["type"] = "message"
	}
	out, _ := json.Marshal(data)
	return out
}

// verifyJWT validates an HS256 JWT and extracts the user_id claim.
func (h *Hub) verifyJWT(tokenStr string) (string, error) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid token format")
	}

	// Verify HMAC-SHA256 signature
	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, h.jwtSecret)
	mac.Write([]byte(signingInput))
	expectedSig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(parts[2]), []byte(expectedSig)) {
		return "", fmt.Errorf("invalid signature")
	}

	// Decode payload
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("payload decode: %w", err)
	}

	var claims struct {
		Sub    string `json:"sub"`
		UserID string `json:"user_id"`
		Exp    int64  `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("claims parse: %w", err)
	}

	if claims.Exp > 0 && time.Now().Unix() > claims.Exp {
		return "", fmt.Errorf("token expired")
	}

	userID := claims.UserID
	if userID == "" {
		userID = claims.Sub
	}
	if userID == "" {
		return "", fmt.Errorf("no user_id in token")
	}

	return userID, nil
}

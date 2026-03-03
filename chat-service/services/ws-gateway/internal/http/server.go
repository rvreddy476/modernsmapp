package http

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	nethttp "net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

type ServerOptions struct {
	JWTSecret      string
	AllowedOrigins []string
	WriteWait      time.Duration
	PongWait       time.Duration
	PingPeriod     time.Duration
	MaxMessageSize int64
}

type Server struct {
	rdb      *redis.Client
	log      *slog.Logger
	opts     ServerOptions
	upgrader websocket.Upgrader
}

func NewServer(rdb *redis.Client, log *slog.Logger, opts ServerOptions) *Server {
	if log == nil {
		log = slog.Default()
	}
	if opts.WriteWait <= 0 {
		opts.WriteWait = 10 * time.Second
	}
	if opts.PongWait <= 0 {
		opts.PongWait = 60 * time.Second
	}
	if opts.PingPeriod <= 0 {
		opts.PingPeriod = (opts.PongWait * 9) / 10
	}
	if opts.MaxMessageSize <= 0 {
		opts.MaxMessageSize = 64 * 1024
	}

	s := &Server{
		rdb:  rdb,
		log:  log,
		opts: opts,
	}
	s.upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *nethttp.Request) bool {
			return isOriginAllowed(r, opts.AllowedOrigins)
		},
	}
	return s
}

func (s *Server) Routes() nethttp.Handler {
	mux := nethttp.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/v1/ws/connect", s.handleWS)
	return loggingMiddleware(s.log, mux)
}

func (s *Server) handleHealth(w nethttp.ResponseWriter, _ *nethttp.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(nethttp.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (s *Server) handleWS(w nethttp.ResponseWriter, r *nethttp.Request) {
	userID, err := authenticateUserFromJWT(r, s.opts.JWTSecret)
	if err != nil {
		s.log.Warn("websocket auth failed", "err", err, "client_ip", readClientIP(r))
		nethttp.Error(w, "unauthorized", nethttp.StatusUnauthorized)
		return
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Warn("websocket upgrade failed", "err", err, "user_id", userID)
		return
	}

	chatChannel := fmt.Sprintf("chat:%s", userID.String())
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Mark user as online (presence TTL 90s; client heartbeat keeps it alive).
	if err := s.rdb.Set(ctx, "presence:"+userID.String(), "1", 90*time.Second).Err(); err != nil {
		s.log.Warn("failed to set presence on connect", "err", err, "user_id", userID)
	}
	// Clear presence when the connection closes.
	defer func() {
		delCtx := context.Background()
		if err := s.rdb.Del(delCtx, "presence:"+userID.String()).Err(); err != nil {
			s.log.Warn("failed to clear presence on disconnect", "err", err, "user_id", userID)
		}
	}()

	// Subscribe to chat messages, new posts, and post interaction updates
	pubsub := s.rdb.Subscribe(ctx, chatChannel, "feed:new_post", "feed:post_update")
	defer func() {
		_ = pubsub.Close()
	}()
	if _, err := pubsub.Receive(ctx); err != nil {
		s.log.Error("redis subscribe failed", "err", err, "user_id", userID, "channel", chatChannel)
		_ = conn.Close()
		return
	}

	s.log.Info("websocket connected", "user_id", userID, "channel", chatChannel, "client_ip", readClientIP(r))
	s.serveConnection(ctx, cancel, conn, pubsub, userID)
}

func (s *Server) serveConnection(
	ctx context.Context,
	cancel context.CancelFunc,
	conn *websocket.Conn,
	pubsub *redis.PubSub,
	userID uuid.UUID,
) {
	outbound := make(chan []byte, 256)

	go s.readLoop(ctx, cancel, conn, pubsub, userID)
	go s.redisLoop(ctx, cancel, pubsub, outbound, userID)
	s.writeLoop(ctx, cancel, conn, outbound, userID)

	_ = conn.Close()
	s.log.Info("websocket disconnected", "user_id", userID)
}

// signalingTypes are WebRTC call signaling message types relayed peer-to-peer via Redis.
var signalingTypes = map[string]bool{
	"call_offer":     true,
	"call_answer":    true,
	"ice_candidate":  true,
	"call_end":       true,
	"call_decline":   true,
	"call_busy":      true,
}

func (s *Server) readLoop(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, pubsub *redis.PubSub, userID uuid.UUID) {
	defer cancel()

	conn.SetReadLimit(s.opts.MaxMessageSize)
	_ = conn.SetReadDeadline(time.Now().Add(s.opts.PongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(s.opts.PongWait))
	})

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				s.log.Warn("websocket read failed", "err", err, "user_id", userID)
			}
			return
		}
		if len(raw) == 0 {
			continue
		}

		var envelope map[string]any
		if json.Unmarshal(raw, &envelope) != nil {
			continue
		}
		msgType, _ := envelope["type"].(string)

		// Handle post room subscriptions (dynamic per-post channels)
		switch msgType {
		case "subscribe_post":
			postID, _ := envelope["post_id"].(string)
			if postID != "" {
				channel := fmt.Sprintf("post:%s", postID)
				if err := pubsub.Subscribe(ctx, channel); err != nil {
					s.log.Warn("post room subscribe failed", "err", err, "user_id", userID, "post_id", postID)
				}
			}
			continue
		case "unsubscribe_post":
			postID, _ := envelope["post_id"].(string)
			if postID != "" {
				channel := fmt.Sprintf("post:%s", postID)
				if err := pubsub.Unsubscribe(ctx, channel); err != nil {
					s.log.Warn("post room unsubscribe failed", "err", err, "user_id", userID, "post_id", postID)
				}
			}
			continue
		}

		// WebRTC signaling relay
		if !signalingTypes[msgType] {
			continue
		}
		targetStr, _ := envelope["target_user_id"].(string)
		targetID, parseErr := uuid.Parse(targetStr)
		if parseErr != nil || targetID == uuid.Nil {
			continue
		}

		// Inject sender_id server-side to prevent spoofing
		envelope["sender_id"] = userID.String()
		relay, _ := json.Marshal(envelope)
		channel := fmt.Sprintf("chat:%s", targetID.String())
		if pubErr := s.rdb.Publish(ctx, channel, string(relay)).Err(); pubErr != nil {
			s.log.Warn("signaling relay failed", "err", pubErr, "user_id", userID, "target", targetID)
		}
	}
}

func (s *Server) redisLoop(
	ctx context.Context,
	cancel context.CancelFunc,
	pubsub *redis.PubSub,
	outbound chan<- []byte,
	userID uuid.UUID,
) {
	defer cancel()
	uid := userID.String()
	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			// For feed/post messages, skip if authored/acted by this connected user
			if msg.Channel == "feed:new_post" || msg.Channel == "feed:post_update" || strings.HasPrefix(msg.Channel, "post:") {
				var data map[string]any
				if json.Unmarshal([]byte(msg.Payload), &data) == nil {
					if pl, ok := data["payload"].(map[string]any); ok {
						if authorID, _ := pl["author_id"].(string); authorID == uid {
							continue
						}
						if actorID, _ := pl["actor_id"].(string); actorID == uid {
							continue
						}
					}
				}
			}
			payload := []byte(msg.Payload)
			if !json.Valid(payload) {
				payload, _ = json.Marshal(map[string]string{"type": "message", "payload": msg.Payload})
			}
			select {
			case outbound <- payload:
			default:
				s.log.Warn("websocket outbound buffer full; closing slow client", "user_id", userID)
				return
			}
		}
	}
}

func (s *Server) writeLoop(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, outbound <-chan []byte, userID uuid.UUID) {
	defer cancel()

	ticker := time.NewTicker(s.opts.PingPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case payload := <-outbound:
			_ = conn.SetWriteDeadline(time.Now().Add(s.opts.WriteWait))
			if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				s.log.Warn("websocket write failed", "err", err, "user_id", userID)
				return
			}
		case <-ticker.C:
			_ = conn.SetWriteDeadline(time.Now().Add(s.opts.WriteWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				s.log.Warn("websocket ping failed", "err", err, "user_id", userID)
				return
			}
		}
	}
}

func isOriginAllowed(r *nethttp.Request, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	for _, item := range allowed {
		value := strings.TrimSpace(item)
		if value == "*" {
			return true
		}
		if strings.EqualFold(value, origin) {
			return true
		}
	}
	return false
}

func readClientIP(r *nethttp.Request) string {
	xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
	if xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	return strings.TrimSpace(r.RemoteAddr)
}

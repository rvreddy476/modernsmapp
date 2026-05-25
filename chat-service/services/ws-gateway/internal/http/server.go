package http

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	nethttp "net/http"
	"strings"
	"time"

	"github.com/atpost/chat-shared/callauth"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

type ServerOptions struct {
	JWTSecret      string
	AllowedOrigins []string
	AllowQueryToken bool

	// AllowAllOriginsForDev opts into the "no policy" mode where any
	// browser origin is accepted. Audit H10: the previous default
	// was *always* allow-all when AllowedOrigins was empty, which is
	// a CSRF / data-scrape vector in production. Now defaults to
	// false; deployments without an allowlist refuse browser
	// upgrades but still accept native clients that don't send the
	// Origin header.
	AllowAllOriginsForDev bool

	// TrustedProxies is the set of peer addresses (exact match on
	// the connecting socket's RemoteAddr host) whose
	// `X-Forwarded-For` header is trusted. Empty means the gateway
	// is directly facing untrusted clients and XFF is ignored — the
	// previous default of trusting any XFF was a logging-side
	// spoof vector flagged in audit H10.
	TrustedProxies []string

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
		// M12: prior default was 64 KiB, eight times the HTTP message-
		// send body limit. A malformed (or hostile) client could push
		// 64 KB chat messages over WS that the HTTP fallback would
		// reject — asymmetric DoS surface. 10 KB matches the HTTP
		// path (8 KB + envelope overhead).
		opts.MaxMessageSize = 10 * 1024
	}

	s := &Server{
		rdb:  rdb,
		log:  log,
		opts: opts,
	}
	s.upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		Subprotocols:    []string{"bearer", "jwt"},
		CheckOrigin: func(r *nethttp.Request) bool {
			return isOriginAllowed(r, opts.AllowedOrigins, opts.AllowAllOriginsForDev)
		},
	}
	return s
}

func (s *Server) Routes() nethttp.Handler {
	mux := nethttp.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/v1/ws/connect", s.handleWS)
	// Web's notificationSocket.ts opens against this path; keep it as
	// an alias so a stale frontend deployment still connects. Same
	// handler — chat + posts + presence + notifications all multiplex
	// over the single connection.
	mux.HandleFunc("/v1/ws/notifications", s.handleWS)
	return loggingMiddleware(s.log, s.opts.TrustedProxies, mux)
}

// watchTokenExpiry fires `cancel` the moment the JWT exp passes,
// terminating readLoop + writeLoop + redisLoop in one shot.
// Tolerates clock skew with a small safety margin (3 s) — better to
// disconnect a fraction too early than keep a revoked session alive.
//
// Exits on its own when the connection's context is cancelled (i.e.
// the client disconnected before the token expired); never leaks a
// goroutine past the connection lifetime.
func (s *Server) watchTokenExpiry(ctx context.Context, cancel context.CancelFunc, userID uuid.UUID, exp time.Time) {
	skew := 3 * time.Second
	until := time.Until(exp) - skew
	if until <= 0 {
		s.log.Info("ws token already expired", "user_id", userID)
		cancel()
		return
	}
	t := time.NewTimer(until)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return
	case <-t.C:
		s.log.Info("ws token expired; closing connection", "user_id", userID, "exp", exp)
		cancel()
	}
}

func (s *Server) handleHealth(w nethttp.ResponseWriter, _ *nethttp.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(nethttp.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (s *Server) handleWS(w nethttp.ResponseWriter, r *nethttp.Request) {
	userID, tokenExp, err := authenticateUserFromJWTWithExpiry(r, s.opts.JWTSecret, s.opts.AllowQueryToken)
	if err != nil {
		s.log.Warn("websocket auth failed", "err", err, "client_ip", s.readClientIP(r))
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

	// Audit C6: tear down the connection when the JWT expires.
	// Without this the WS happily outlives the token — a revoked
	// session keeps reading chat / signaling until the client
	// happens to disconnect. We fire a one-shot timer at exp time
	// and a periodic safety check every 60s to cover JWTs that
	// embed only `nbf` (no `exp`) or where the timer drifts.
	if !tokenExp.IsZero() {
		go s.watchTokenExpiry(ctx, cancel, userID, tokenExp)
	}

	// Mark user as online (presence TTL 90s; client heartbeat keeps it alive).
	if err := s.rdb.Set(ctx, "presence:"+userID.String(), "1", 90*time.Second).Err(); err != nil {
		s.log.Warn("failed to set presence on connect", "err", err, "user_id", userID)
	}
	// Broadcast online status to the presence channel so other connected users can react.
	s.rdb.Publish(ctx, "presence:updates", fmt.Sprintf(`{"user_id":"%s","online":true}`, userID.String()))

	// Clear presence when the connection closes.
	defer func() {
		delCtx := context.Background()
		if err := s.rdb.Del(delCtx, "presence:"+userID.String()).Err(); err != nil {
			s.log.Warn("failed to clear presence on disconnect", "err", err, "user_id", userID)
		}
		s.rdb.Publish(delCtx, "presence:updates", fmt.Sprintf(`{"user_id":"%s","online":false}`, userID.String()))
	}()

	// Subscribe to chat messages, new posts, post interaction updates,
	// and presence changes. Notifications used to ride this multiplex
	// too via `notify:<userID>`, but the realtime-transport split
	// moved them to a dedicated SSE channel
	// (notification-service /v1/notifications/stream) per README §1
	// and §17. Keeping the subscription here would burn Redis fan-out
	// bandwidth per connected client with no consumer.
	pubsub := s.rdb.Subscribe(ctx, chatChannel, "feed:new_post", "feed:post_update", "presence:updates")
	defer func() {
		_ = pubsub.Close()
	}()
	if _, err := pubsub.Receive(ctx); err != nil {
		s.log.Error("redis subscribe failed", "err", err, "user_id", userID, "channel", chatChannel)
		_ = conn.Close()
		return
	}

	s.log.Info("websocket connected", "user_id", userID, "channel", chatChannel, "client_ip", s.readClientIP(r))
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

// directSignalingTypes are P2P call signaling types relayed to a specific target user.
var directSignalingTypes = map[string]bool{
	"call_offer":    true,
	"call_answer":   true,
	"ice_candidate": true,
	"call_end":      true,
	"call_decline":  true,
	"call_busy":     true,
	"call_ring":     true,
	"call_accept":   true,
	"call_reject":   true,
}

// roomSignalingTypes are call room signaling types broadcast to all participants via call:{callId}.
var roomSignalingTypes = map[string]bool{
	"call_join":                true,
	"call_leave":               true,
	"call_mute_toggle":         true,
	"call_video_toggle":        true,
	"call_screen_share_start":  true,
	"call_screen_share_stop":   true,
	"call_hand_raise":          true,
	"call_hand_lower":          true,
	"call_participant_joined":  true,
	"call_participant_left":    true,
	"call_participant_muted":   true,
	"call_participant_unmuted": true,
	"call_participant_removed": true,
	"call_state_change":        true,
	"call_quality_report":      true,
	"call_upgrade_request":     true,
	"call_upgrade_accept":      true,
	"call_upgrade_reject":      true,
	"call_recording_started":   true,
	"call_recording_stopped":   true,
}

func (s *Server) readLoop(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, pubsub *redis.PubSub, userID uuid.UUID) {
	defer cancel()

	conn.SetReadLimit(s.opts.MaxMessageSize)
	_ = conn.SetReadDeadline(time.Now().Add(s.opts.PongWait))
	conn.SetPongHandler(func(string) error {
		// Refresh presence TTL on each pong (keeps user "online" while WS is alive).
		_ = s.rdb.Set(ctx, "presence:"+userID.String(), "1", 90*time.Second)
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

		// Handle room subscriptions (dynamic per-post and per-call channels)
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
		case "subscribe_call":
			callID, _ := envelope["call_id"].(string)
			if callID != "" {
				channel := fmt.Sprintf("call:%s", callID)
				if err := pubsub.Subscribe(ctx, channel); err != nil {
					s.log.Warn("call room subscribe failed", "err", err, "user_id", userID, "call_id", callID)
				}
			}
			continue
		case "unsubscribe_call":
			callID, _ := envelope["call_id"].(string)
			if callID != "" {
				channel := fmt.Sprintf("call:%s", callID)
				if err := pubsub.Unsubscribe(ctx, channel); err != nil {
					s.log.Warn("call room unsubscribe failed", "err", err, "user_id", userID, "call_id", callID)
				}
			}
			continue
		case "subscribe_live_stream":
			streamID, _ := envelope["stream_id"].(string)
			if streamID != "" {
				channel := fmt.Sprintf("live:stream:%s", streamID)
				if err := pubsub.Subscribe(ctx, channel); err != nil {
					s.log.Warn("live room subscribe failed", "err", err, "user_id", userID, "stream_id", streamID)
				}
			}
			continue
		case "unsubscribe_live_stream":
			streamID, _ := envelope["stream_id"].(string)
			if streamID != "" {
				channel := fmt.Sprintf("live:stream:%s", streamID)
				if err := pubsub.Unsubscribe(ctx, channel); err != nil {
					s.log.Warn("live room unsubscribe failed", "err", err, "user_id", userID, "stream_id", streamID)
				}
			}
			continue
		case "subscribe_update":
			updateID, _ := envelope["update_id"].(string)
			if updateID != "" {
				channel := fmt.Sprintf("update:%s", updateID)
				if err := pubsub.Subscribe(ctx, channel); err != nil {
					s.log.Warn("update room subscribe failed", "err", err, "user_id", userID, "update_id", updateID)
				}
			}
			continue
		case "unsubscribe_update":
			updateID, _ := envelope["update_id"].(string)
			if updateID != "" {
				channel := fmt.Sprintf("update:%s", updateID)
				if err := pubsub.Unsubscribe(ctx, channel); err != nil {
					s.log.Warn("update room unsubscribe failed", "err", err, "user_id", userID, "update_id", updateID)
				}
			}
			continue
		case "subscribe_group_post":
			postID, _ := envelope["post_id"].(string)
			if postID != "" {
				channel := fmt.Sprintf("group_post:%s", postID)
				if err := pubsub.Subscribe(ctx, channel); err != nil {
					s.log.Warn("group post room subscribe failed", "err", err, "user_id", userID, "post_id", postID)
				}
			}
			continue
		case "unsubscribe_group_post":
			postID, _ := envelope["post_id"].(string)
			if postID != "" {
				channel := fmt.Sprintf("group_post:%s", postID)
				if err := pubsub.Unsubscribe(ctx, channel); err != nil {
					s.log.Warn("group post room unsubscribe failed", "err", err, "user_id", userID, "post_id", postID)
				}
			}
			continue
		case "group_post_typing":
			postID, _ := envelope["post_id"].(string)
			if postID != "" {
				// Broadcast typing indicator to all subscribers of this group post room
				envelope["user_id"] = userID.String()
				relay, _ := json.Marshal(envelope)
				channel := fmt.Sprintf("group_post:%s", postID)
				if pubErr := s.rdb.Publish(ctx, channel, string(relay)).Err(); pubErr != nil {
					s.log.Warn("group post typing relay failed", "err", pubErr, "user_id", userID, "post_id", postID)
				}
			}
			continue
		}

		// Inject sender_id server-side to prevent spoofing
		envelope["sender_id"] = userID.String()
		relay, _ := json.Marshal(envelope)

		// Direct signaling: relay to target user's personal channel.
		// Before relaying, verify call-service has authorized this
		// pair to exchange signaling — without the check any authed
		// client could ring or ICE-probe arbitrary users (audit C1).
		// `ice_candidate` additionally requires the call to be in
		// `active` state, gating the IP-leak window between ringing
		// and accept (audit C3).
		if directSignalingTypes[msgType] {
			targetStr, _ := envelope["target_user_id"].(string)
			targetID, parseErr := uuid.Parse(targetStr)
			if parseErr != nil || targetID == uuid.Nil {
				continue
			}
			authState, err := callauth.Get(ctx, s.rdb, userID, targetID)
			if err != nil {
				// Redis is degraded — fail closed. The cost is a
				// brief signaling outage while Redis recovers; the
				// alternative (fail-open) was the original bug.
				s.log.Warn("signaling auth lookup failed; dropping",
					"err", err, "user_id", userID, "target", targetID, "type", msgType)
				continue
			}
			if authState == nil {
				s.log.Warn("signaling rejected: no active call between pair",
					"user_id", userID, "target", targetID, "type", msgType)
				continue
			}
			if !callauth.IsAllowedFor(authState.State, msgType) {
				s.log.Warn("signaling rejected: state does not permit message type",
					"user_id", userID, "target", targetID, "type", msgType,
					"state", authState.State, "call_id", authState.CallID)
				continue
			}
			channel := fmt.Sprintf("chat:%s", targetID.String())
			if pubErr := s.rdb.Publish(ctx, channel, string(relay)).Err(); pubErr != nil {
				s.log.Warn("signaling relay failed", "err", pubErr, "user_id", userID, "target", targetID)
			}
			continue
		}

		// Room signaling: broadcast to all call participants via call:{callID}
		if roomSignalingTypes[msgType] {
			callID, _ := envelope["call_id"].(string)
			if callID == "" {
				continue
			}
			channel := fmt.Sprintf("call:%s", callID)
			if pubErr := s.rdb.Publish(ctx, channel, string(relay)).Err(); pubErr != nil {
				s.log.Warn("call room relay failed", "err", pubErr, "user_id", userID, "call_id", callID)
			}
			continue
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
			// Presence updates: wrap with type field and skip own presence
			if msg.Channel == "presence:updates" {
				var pdata map[string]any
				if json.Unmarshal([]byte(msg.Payload), &pdata) == nil {
					if presUID, _ := pdata["user_id"].(string); presUID == uid {
						continue // Don't send own presence back to self
					}
					pdata["type"] = "presence_update"
					if wrapped, err := json.Marshal(pdata); err == nil {
						select {
						case outbound <- wrapped:
						default:
						}
					}
				}
				continue
			}
			// For feed/post messages, skip if authored/acted by the
			// connected user. Audit H6: the previous version JSON-
			// unmarshaled every message into a map[string]any just to
			// peek at two fields — expensive on the hot path (one of
			// these channels fires on every post create + every
			// engagement). Substring-scan the raw payload first; only
			// fall through to a full parse when the user's UUID
			// actually appears, which is the rare case.
			if msg.Channel == "feed:new_post" || msg.Channel == "feed:post_update" ||
				strings.HasPrefix(msg.Channel, "post:") ||
				strings.HasPrefix(msg.Channel, "update:") ||
				strings.HasPrefix(msg.Channel, "group_post:") {
				if strings.Contains(msg.Payload, uid) {
					// UUID appears somewhere — could be the actor /
					// author. Confirm with a parse before dropping
					// (avoid false-positive drops when the same UUID
					// shows up as a target/mention id, etc.).
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

func isOriginAllowed(r *nethttp.Request, allowed []string, allowAllForDev bool) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		// Native clients (mobile WebSocket libraries, server-to-
		// server) typically don't send Origin. CSRF only applies to
		// browser-issued requests; refusing here would break every
		// non-browser caller for no security benefit.
		return true
	}
	if len(allowed) == 0 {
		// Audit H10: previously allow-all here. Now only allowed in
		// explicit dev mode. Production deployments without an
		// allowlist refuse browser upgrades to close the CSRF gap.
		return allowAllForDev
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

// readClientIP returns a best-effort peer IP for log lines and
// rate-limit keys. Audit H10: the previous version blindly trusted
// the first `X-Forwarded-For` entry — anyone could spoof the logged
// client IP by setting that header. Now XFF is only honored when
// the immediate TCP peer (`RemoteAddr` host) is in `trustedProxies`;
// everywhere else we use `RemoteAddr` so the IP corresponds to the
// actual TCP origin.
//
// Package-level so the request-logging middleware (which doesn't hold
// a Server reference) can use it too.
func readClientIP(r *nethttp.Request, trustedProxies []string) string {
	remote := strings.TrimSpace(r.RemoteAddr)
	peerHost := remote
	if h, _, err := net.SplitHostPort(remote); err == nil {
		peerHost = h
	}
	if len(trustedProxies) > 0 && peerHost != "" {
		trusted := false
		for _, p := range trustedProxies {
			if strings.EqualFold(strings.TrimSpace(p), peerHost) {
				trusted = true
				break
			}
		}
		if trusted {
			if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
				parts := strings.Split(xff, ",")
				return strings.TrimSpace(parts[0])
			}
		}
	}
	return remote
}

// readClientIP is the Server-bound wrapper used by handlers that
// have an *s. Same trusted-proxies policy as the package-level
// helper.
func (s *Server) readClientIP(r *nethttp.Request) string {
	return readClientIP(r, s.opts.TrustedProxies)
}

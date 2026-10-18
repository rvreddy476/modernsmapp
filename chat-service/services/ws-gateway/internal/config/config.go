package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPPort         string
	HTTPReadTimeout  time.Duration
	HTTPWriteTimeout time.Duration
	HTTPIdleTimeout  time.Duration
	RedisAddr        string
	JWTSecret        string
	// C7 — kid + previous-secret rotation knobs. See aws_prep_sprint_2026_06.
	JWTKID            string
	JWTSecretPrevious string
	JWTKIDPrevious    string
	// RS256 (optional, additive): public key + kid to verify RS256 tokens.
	JWTPublicKeyPEM   string
	JWTRS256KID       string
	AllowedOrigins    []string
	WSAllowQueryToken bool
	WSWriteWait       time.Duration
	WSPongWait        time.Duration
	WSPingPeriod      time.Duration
	WSMaxMessageSize  int64
	// EntitlementSecret verifies owner-issued conversation-room tokens
	// (production chat pass §5.3). Shared with message-service; empty
	// disables entitled room subscriptions entirely.
	EntitlementSecret string
	// Post rooms (live comment threads): owner-checked against post-service.
	PostRoomsEnabled   bool
	PostServiceURL     string
	InternalServiceKey string
	// Live rooms (live:stream:<id>): owner-checked against live-service-v2's
	// internal viewer route. Off unless ENABLE_LIVE_ROOMS=true.
	LiveRoomsEnabled bool
	LiveServiceV2URL string

	// Chat parity §4 (18 Oct 2026).
	//
	// ConversationFramesEnabled (WS_CONVERSATION_FRAMES_ENABLED, default ON)
	// admits conversation.enter/heartbeat/leave and typing.start/typing.stop
	// for rooms the connection holds an entitled convroom subscription to.
	// It deliberately does NOT turn on the broader scoped-rooms gate, which
	// would also admit unchecked call/update/group-post rooms.
	ConversationFramesEnabled bool
	// MessageServiceURL receives typing transitions (POST
	// /v1/chat/conversations/:id/typing with the socket's own bearer token);
	// message-service applies send_typing_indicators / paused policy.
	MessageServiceURL string
	// PresenceFanoutEnabled (WS_PRESENCE_FANOUT_ENABLED, default ON) pushes
	// presence_update to permitted DM peers. It also needs ChatPostgresDSN
	// (CHAT_POSTGRES_DSN, read-only use of chat_db for the DM peer list) and
	// GraphServiceURL (see_online_status / see_last_seen decisions). Missing
	// either leaves fan-out off: nothing is disclosed.
	PresenceFanoutEnabled bool
	ChatPostgresDSN       string
	GraphServiceURL       string
}

func (c *Config) ValidateProduction(production bool) error {
	if !production {
		return nil
	}
	if c.WSAllowQueryToken {
		return errors.New("WS_ALLOW_QUERY_TOKEN must be false in production")
	}
	if len(c.AllowedOrigins) == 0 {
		return errors.New("ALLOWED_ORIGINS is required in production")
	}
	for _, origin := range c.AllowedOrigins {
		if origin == "*" {
			return errors.New("wildcard ALLOWED_ORIGINS is forbidden in production")
		}
	}
	return nil
}

func Load() *Config {
	pongWait := getEnvDuration("WS_PONG_WAIT", 60*time.Second)
	pingPeriod := getEnvDuration("WS_PING_PERIOD", (pongWait*9)/10)
	return &Config{
		HTTPPort:                  getEnv("HTTP_PORT", "8093"),
		HTTPReadTimeout:           getEnvDuration("HTTP_READ_TIMEOUT", 15*time.Second),
		HTTPWriteTimeout:          getEnvDuration("HTTP_WRITE_TIMEOUT", 15*time.Second),
		HTTPIdleTimeout:           getEnvDuration("HTTP_IDLE_TIMEOUT", 60*time.Second),
		RedisAddr:                 getEnv("REDIS_ADDR", "localhost:6379"),
		JWTSecret:                 getEnv("JWT_SECRET", ""),
		JWTKID:                    getEnv("JWT_KID", "v1"),
		JWTSecretPrevious:         getEnv("JWT_SECRET_PREVIOUS", ""),
		JWTKIDPrevious:            getEnv("JWT_KID_PREVIOUS", ""),
		JWTPublicKeyPEM:           getEnv("JWT_PUBLIC_KEY_PEM", ""),
		JWTRS256KID:               getEnv("JWT_RS256_KID", "rsa-1"),
		AllowedOrigins:            splitAndClean(getEnv("ALLOWED_ORIGINS", "")),
		WSAllowQueryToken:         getEnvBool("WS_ALLOW_QUERY_TOKEN", true),
		WSWriteWait:               getEnvDuration("WS_WRITE_WAIT", 10*time.Second),
		WSPongWait:                pongWait,
		WSPingPeriod:              pingPeriod,
		WSMaxMessageSize:          getEnvInt64("WS_MAX_MESSAGE_SIZE", 64*1024),
		EntitlementSecret:         getEnv("CHAT_ENTITLEMENT_SECRET", ""),
		PostRoomsEnabled:          getEnvBool("WS_POST_ROOMS_ENABLED", false),
		PostServiceURL:            getEnv("POST_SERVICE_URL", "http://post-service:8084"),
		InternalServiceKey:        getEnv("INTERNAL_SERVICE_KEY", ""),
		LiveRoomsEnabled:          getEnvBool("ENABLE_LIVE_ROOMS", false),
		LiveServiceV2URL:          getEnv("LIVE_V2_SERVICE_URL", "http://live-service-v2:8117"),
		ConversationFramesEnabled: getEnvBool("WS_CONVERSATION_FRAMES_ENABLED", true),
		MessageServiceURL:         getEnv("MESSAGE_SERVICE_URL", "http://chat-message-service:8092"),
		PresenceFanoutEnabled:     getEnvBool("WS_PRESENCE_FANOUT_ENABLED", true),
		ChatPostgresDSN:           getEnv("CHAT_POSTGRES_DSN", ""),
		GraphServiceURL:           getEnv("GRAPH_SERVICE_URL", "http://graph-service:8083"),
	}
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func getEnvInt64(key string, def int64) int64 {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		parsed, err := strconv.ParseInt(v, 10, 64)
		if err == nil {
			return parsed
		}
	}
	return def
}

func getEnvBool(key string, def bool) bool {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		parsed, err := strconv.ParseBool(v)
		if err == nil {
			return parsed
		}
	}
	return def
}

func splitAndClean(val string) []string {
	parts := strings.Split(val, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

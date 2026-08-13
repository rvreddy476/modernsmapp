package config

import (
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPPort               string
	PostgresDSN            string
	RedisAddr              string
	KafkaBrokers           []string
	KafkaLifecycleTopic    string
	KafkaNotificationTopic string
	KafkaAnalyticsTopic    string
	JWTSecret              string
	// C7 — kid + previous-secret rotation knobs.
	JWTKID             string
	JWTSecretPrevious  string
	JWTKIDPrevious     string
	TrustedProxies     []string
	OutboxPollInterval time.Duration

	// SFU provider (LiveKit)
	LiveKitHost      string
	LiveKitAPIKey    string
	LiveKitAPISecret string
	ICEServersJSON   string

	// Call timeouts
	RingTimeoutSeconds     int
	InviteExpirySeconds    int
	MaxCallDurationMinutes int
	ReconnectGraceSeconds  int
	CallsEnabled           bool
	GraphServiceURL        string
	InternalServiceKey     string
}

func Load() *Config {
	return &Config{
		HTTPPort:               getEnv("HTTP_PORT", "8097"),
		PostgresDSN:            getEnv("POSTGRES_DSN", "postgres://postgres:postgres@localhost:5432/call_db?sslmode=disable"),
		RedisAddr:              getEnv("REDIS_ADDR", "localhost:6379"),
		KafkaBrokers:           splitAndClean(getEnv("KAFKA_BROKERS", "localhost:9092")),
		KafkaLifecycleTopic:    getEnv("KAFKA_LIFECYCLE_TOPIC", "call.lifecycle"),
		KafkaNotificationTopic: getEnv("KAFKA_NOTIFICATION_TOPIC", "call.notifications"),
		KafkaAnalyticsTopic:    getEnv("KAFKA_ANALYTICS_TOPIC", "call.analytics"),
		JWTSecret:              getEnv("JWT_SECRET", ""),
		JWTKID:                 getEnv("JWT_KID", "v1"),
		JWTSecretPrevious:      getEnv("JWT_SECRET_PREVIOUS", ""),
		JWTKIDPrevious:         getEnv("JWT_KID_PREVIOUS", ""),
		TrustedProxies:         splitAndClean(getEnv("TRUSTED_PROXIES", "")),
		OutboxPollInterval:     getEnvDuration("OUTBOX_POLL_INTERVAL", 1*time.Second),
		LiveKitHost:            getFirstEnv([]string{"LIVEKIT_HOST", "LIVEKIT_URL"}, ""),
		LiveKitAPIKey:          getEnv("LIVEKIT_API_KEY", ""),
		LiveKitAPISecret:       getEnv("LIVEKIT_API_SECRET", ""),
		ICEServersJSON:         getEnv("ICE_SERVERS_JSON", ""),
		RingTimeoutSeconds:     getEnvInt("RING_TIMEOUT_SECONDS", 30),
		InviteExpirySeconds:    getEnvInt("INVITE_EXPIRY_SECONDS", 60),
		MaxCallDurationMinutes: getEnvInt("MAX_CALL_DURATION_MINUTES", 240),
		ReconnectGraceSeconds:  getEnvInt("RECONNECT_GRACE_SECONDS", 30),
		CallsEnabled:           getEnvBool("CALLS_ENABLED", false),
		GraphServiceURL:        strings.TrimSpace(os.Getenv("GRAPH_SERVICE_URL")),
		InternalServiceKey:     strings.TrimSpace(os.Getenv("INTERNAL_SERVICE_KEY")),
	}
}

// ValidateCallEnablement ensures the launch kill switch cannot be turned on
// into an abuse-prone stub configuration. Disabled calls require no provider.
func (c *Config) ValidateCallEnablement() error {
	if !c.CallsEnabled {
		return nil
	}
	switch {
	case c.GraphServiceURL == "":
		return errors.New("GRAPH_SERVICE_URL is required when calls are enabled")
	case c.InternalServiceKey == "":
		return errors.New("INTERNAL_SERVICE_KEY is required when calls are enabled")
	case strings.TrimSpace(c.LiveKitHost) == "":
		return errors.New("LIVEKIT_HOST is required when calls are enabled")
	case strings.TrimSpace(c.LiveKitAPIKey) == "":
		return errors.New("LIVEKIT_API_KEY is required when calls are enabled")
	case strings.TrimSpace(c.LiveKitAPISecret) == "":
		return errors.New("LIVEKIT_API_SECRET is required when calls are enabled")
	case !containsTURNRelay(c.ICEServersJSON):
		return errors.New("ICE_SERVERS_JSON must contain a TURN or TURNS relay when calls are enabled")
	}
	return nil
}

func containsTURNRelay(raw string) bool {
	var servers []struct {
		URLs any `json:"urls"`
	}
	if json.Unmarshal([]byte(raw), &servers) != nil {
		return false
	}
	for _, server := range servers {
		switch urls := server.URLs.(type) {
		case string:
			if isTURNURL(urls) {
				return true
			}
		case []any:
			for _, candidate := range urls {
				if url, ok := candidate.(string); ok && isTURNURL(url) {
					return true
				}
			}
		}
	}
	return false
}

func isTURNURL(raw string) bool {
	value := strings.ToLower(strings.TrimSpace(raw))
	return strings.HasPrefix(value, "turn:") || strings.HasPrefix(value, "turns:")
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getFirstEnv(keys []string, def string) string {
	for _, key := range keys {
		if v := os.Getenv(key); v != "" {
			return v
		}
	}
	return def
}

func splitAndClean(val string) []string {
	parts := strings.Split(val, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func getEnvDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		var n int
		for _, c := range v {
			if c >= '0' && c <= '9' {
				n = n*10 + int(c-'0')
			} else {
				return def
			}
		}
		return n
	}
	return def
}

func getEnvBool(key string, def bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return def
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return def
	}
	return parsed
}

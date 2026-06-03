package config

import (
	"os"
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
	JWTKID                 string
	JWTSecretPrevious      string
	JWTKIDPrevious         string
	TrustedProxies         []string
	OutboxPollInterval     time.Duration

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
	}
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

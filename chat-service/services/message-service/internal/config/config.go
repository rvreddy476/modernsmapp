package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPPort       string
	PostgresDSN    string
	ScyllaHosts    []string
	ScyllaKeyspace string
	RedisAddr      string
	KafkaBrokers   []string
	KafkaTopic     string
	JWTSecret      string
	// C7 — kid + previous-secret rotation knobs.
	JWTKID               string
	JWTSecretPrevious    string
	JWTKIDPrevious       string
	UserServiceURL       string
	IdentityUserURL      string
	GraphServiceURL      string
	MediaServiceURL      string
	InviteLinkBaseURL    string
	InternalServiceKey   string
	TrustedProxies       []string
	OutboxPollInterval   time.Duration
	CacheTTL             time.Duration
	IdentityKafkaTopic   string
	IdentityKafkaGroupID string
	SocialKafkaTopic     string
	SocialKafkaGroupID   string
	DatingKafkaTopic     string
	DatingKafkaGroupID   string
	DatingServiceURL     string
	// Chat parity (2026-10-18). SearchServiceURL empty: /v1/chat/search
	// returns no people and no messages. TrustSafetyURL: message reports.
	SearchServiceURL string
	TrustSafetyURL   string

	// Disappearing messages (20 Oct 2026): the expiry worker. ON unless
	// DISAPPEARING_MESSAGES_WORKER_ENABLED is false/0/off.
	DisappearingWorkerEnabled bool
	DisappearingPollInterval  time.Duration
	DisappearingBatch         int

	// DM consent model (20 Oct 2026): the 30-day message-request expiry
	// sweep. ON unless MESSAGE_REQUEST_EXPIRY_WORKER_ENABLED is false/0/off.
	RequestExpiryWorkerEnabled bool
	RequestExpiryPollInterval  time.Duration
	RequestExpiryBatch         int

	// Field encryption at rest (22 Oct 2026): published outbox rows are
	// deleted after OUTBOX_RETENTION (default 168h = 7 days), checked every
	// OUTBOX_RETENTION_INTERVAL (default 1h). The key ring itself is read by
	// internal/fieldkeys (CHAT_KMS_KEY_ID or CHAT_FIELD_KEYS).
	OutboxRetention         time.Duration
	OutboxRetentionInterval time.Duration
}

func Load() *Config {
	return &Config{
		HTTPPort:          getEnv("HTTP_PORT", "8092"),
		PostgresDSN:       getEnv("POSTGRES_DSN", "postgres://postgres:postgres@localhost:5432/chat_db?sslmode=disable"),
		ScyllaHosts:       splitAndClean(getEnv("SCYLLA_HOSTS", "localhost")),
		ScyllaKeyspace:    getEnv("SCYLLA_KEYSPACE", "chatservice"),
		RedisAddr:         getEnv("REDIS_ADDR", "localhost:6379"),
		KafkaBrokers:      splitAndClean(getEnv("KAFKA_BROKERS", "localhost:9092")),
		KafkaTopic:        getEnv("KAFKA_TOPIC", "chat.events.v1"),
		JWTSecret:         getEnv("JWT_SECRET", ""),
		JWTKID:            getEnv("JWT_KID", "v1"),
		JWTSecretPrevious: getEnv("JWT_SECRET_PREVIOUS", ""),
		JWTKIDPrevious:    getEnv("JWT_KID_PREVIOUS", ""),
		UserServiceURL:    getEnv("USER_SERVICE_URL", "http://user-service:8082"),
		IdentityUserURL:   getEnv("IDENTITY_USER_SERVICE_URL", "http://identity-user:8110"),
		GraphServiceURL:   getEnv("GRAPH_SERVICE_URL", "http://graph-service:8083"),
		MediaServiceURL:   getEnv("MEDIA_SERVICE_URL", "http://media-service:8087"),
		// Chat-app pass: public base for group invite links; the 10-char code
		// is appended. Android registers this host for the join deep link.
		InviteLinkBaseURL:    getEnv("INVITE_LINK_BASE_URL", "https://atpost.app/chat/join/"),
		InternalServiceKey:   getEnv("INTERNAL_SERVICE_KEY", ""),
		TrustedProxies:       splitAndClean(getEnv("TRUSTED_PROXIES", "")),
		OutboxPollInterval:   getEnvDuration("OUTBOX_POLL_INTERVAL", 1*time.Second),
		CacheTTL:             getEnvDuration("CACHE_TTL", 5*time.Minute),
		IdentityKafkaTopic:   getEnv("IDENTITY_KAFKA_TOPIC", "identity.events.v1"),
		IdentityKafkaGroupID: getEnv("IDENTITY_KAFKA_GROUP_ID", "chat-service-identity"),
		SocialKafkaTopic:     getEnv("SOCIAL_KAFKA_TOPIC", "social.events.v1"),
		SocialKafkaGroupID:   getEnv("SOCIAL_KAFKA_GROUP_ID", "chat-service-social"),
		// P0-3/9: dating-events consumer. The dating-service publishes
		// match.closed + match.expired to dating-events; chat-service
		// flips the matching conversation's closed_at so the send-path
		// gate refuses subsequent messages.
		DatingKafkaTopic:   getEnv("DATING_KAFKA_TOPIC", "dating-events"),
		DatingKafkaGroupID: getEnv("DATING_KAFKA_GROUP_ID", "chat-service-dating"),
		// Dating lane D4: first-message notifications go to
		// /v1/dating/internal/matches/{id}/first-message on this base.
		DatingServiceURL: getEnv("DATING_SERVICE_URL", "http://dating-service:8112"),
		SearchServiceURL: getEnv("SEARCH_SERVICE_URL", ""),
		TrustSafetyURL:   getEnv("TRUST_SAFETY_SERVICE_URL", "http://trust-safety-service:8091"),

		DisappearingWorkerEnabled: getEnvBool("DISAPPEARING_MESSAGES_WORKER_ENABLED", true),
		DisappearingPollInterval:  getEnvDuration("DISAPPEARING_MESSAGES_POLL_INTERVAL", 30*time.Second),
		DisappearingBatch:         getEnvInt("DISAPPEARING_MESSAGES_BATCH", 100),

		RequestExpiryWorkerEnabled: getEnvBool("MESSAGE_REQUEST_EXPIRY_WORKER_ENABLED", true),
		RequestExpiryPollInterval:  getEnvDuration("MESSAGE_REQUEST_EXPIRY_POLL_INTERVAL", 10*time.Minute),
		RequestExpiryBatch:         getEnvInt("MESSAGE_REQUEST_EXPIRY_BATCH", 200),

		OutboxRetention:         getEnvDuration("OUTBOX_RETENTION", 7*24*time.Hour),
		OutboxRetentionInterval: getEnvDuration("OUTBOX_RETENTION_INTERVAL", time.Hour),
	}
}

func getEnvBool(key string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "":
		return def
	case "0", "false", "off", "no":
		return false
	case "1", "true", "on", "yes":
		return true
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
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

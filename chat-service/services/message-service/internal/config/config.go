package config

import (
	"os"
	"strings"
	"time"
)

type Config struct {
	HTTPPort             string
	PostgresDSN          string
	ScyllaHosts          []string
	ScyllaKeyspace       string
	RedisAddr            string
	KafkaBrokers         []string
	KafkaTopic           string
	JWTSecret            string
	UserServiceURL       string
	GraphServiceURL      string
	InternalServiceKey   string
	TrustedProxies       []string
	OutboxPollInterval   time.Duration
	CacheTTL             time.Duration
	IdentityKafkaTopic   string
	IdentityKafkaGroupID string
	SocialKafkaTopic     string
	SocialKafkaGroupID   string
}

func Load() *Config {
	return &Config{
		HTTPPort:             getEnv("HTTP_PORT", "8092"),
		PostgresDSN:          getEnv("POSTGRES_DSN", "postgres://postgres:postgres@localhost:5432/chat_db?sslmode=disable"),
		ScyllaHosts:          splitAndClean(getEnv("SCYLLA_HOSTS", "localhost")),
		ScyllaKeyspace:       getEnv("SCYLLA_KEYSPACE", "chatservice"),
		RedisAddr:            getEnv("REDIS_ADDR", "localhost:6379"),
		KafkaBrokers:         splitAndClean(getEnv("KAFKA_BROKERS", "localhost:9092")),
		KafkaTopic:           getEnv("KAFKA_TOPIC", "chat.events.v1"),
		JWTSecret:            getEnv("JWT_SECRET", ""),
		UserServiceURL:       getEnv("USER_SERVICE_URL", "http://user-service:8082"),
		GraphServiceURL:      getEnv("GRAPH_SERVICE_URL", "http://graph-service:8083"),
		InternalServiceKey:   getEnv("INTERNAL_SERVICE_KEY", ""),
		TrustedProxies:       splitAndClean(getEnv("TRUSTED_PROXIES", "")),
		OutboxPollInterval:   getEnvDuration("OUTBOX_POLL_INTERVAL", 1*time.Second),
		CacheTTL:             getEnvDuration("CACHE_TTL", 5*time.Minute),
		IdentityKafkaTopic:   getEnv("IDENTITY_KAFKA_TOPIC", "identity.events.v1"),
		IdentityKafkaGroupID: getEnv("IDENTITY_KAFKA_GROUP_ID", "chat-service-identity"),
		SocialKafkaTopic:     getEnv("SOCIAL_KAFKA_TOPIC", "social.events.v1"),
		SocialKafkaGroupID:   getEnv("SOCIAL_KAFKA_GROUP_ID", "chat-service-social"),
	}
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

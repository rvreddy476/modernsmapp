package config

import (
	"os"
	"strings"
	"time"
)

type Config struct {
	HTTPPort     string
	PostgresDSN  string
	RedisAddr    string
	KafkaBrokers []string
	KafkaTopic   string
	KafkaGroupID string
	CacheTTL     time.Duration
	JWTSecret    string
	TrustedProxies []string
}

func Load() *Config {
	return &Config{
		HTTPPort:     getEnv("HTTP_PORT", "8082"),
		PostgresDSN:  getEnv("POSTGRES_DSN", "postgres://postgres:PRvr%4019910830@localhost:5432/identity_db?sslmode=disable"),
		RedisAddr:    getEnv("REDIS_ADDR", "localhost:6379"),
		KafkaBrokers: splitAndClean(getEnv("KAFKA_BROKERS", "localhost:9092")),
		KafkaTopic:   getEnv("KAFKA_TOPIC", "identity.events.v1"),
		KafkaGroupID: getEnv("KAFKA_GROUP_ID", "user-service-group"),
		CacheTTL:     getEnvDuration("CACHE_TTL", 5*time.Minute),
		JWTSecret:    getEnv("JWT_SECRET", "dev_secret_change_me"),
		TrustedProxies: splitAndClean(getEnv("TRUSTED_PROXIES", "")),
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

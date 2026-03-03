package config

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds runtime configuration sourced from environment variables.
type Config struct {
	HTTPPort        string
	PostgresDSN     string
	RedisAddr       string
	KafkaBrokers    []string
	KafkaTopic      string
	OTPBypassCode   string
	OTPDigits       int
	OTPExpiry       time.Duration
	OTPMaxAttempts  int
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	JWTSecret       string
	CookieDomain    string
	CookieSecure    bool
	TrustedProxies  []string
	TwoFAIssuer     string
	FrontendURL     string
	OAuth           *OAuthConfig
}

// Load reads configuration from environment variables and applies sensible defaults for local development.
func Load() *Config {
	cfg := &Config{
		HTTPPort:        getEnv("HTTP_PORT", "8081"),
		PostgresDSN:     getEnv("POSTGRES_DSN", "postgres://postgres:PRvr%4019910830@localhost:5432/identity_db?sslmode=disable"),
		RedisAddr:       getEnv("REDIS_ADDR", "localhost:6379"),
		KafkaBrokers:    splitAndClean(getEnv("KAFKA_BROKERS", "localhost:9092")),
		KafkaTopic:      getEnv("KAFKA_TOPIC", "identity.events.v1"),
		OTPBypassCode:   getEnv("OTP_BYPASS_CODE", ""),
		OTPDigits:       getEnvInt("OTP_DIGITS", 6),
		OTPExpiry:       getEnvDuration("OTP_EXPIRY", 5*time.Minute),
		OTPMaxAttempts:  getEnvInt("OTP_MAX_ATTEMPTS", 5),
		AccessTokenTTL:  getEnvDuration("ACCESS_TOKEN_TTL", 15*time.Minute),
		RefreshTokenTTL: getEnvDuration("REFRESH_TOKEN_TTL", 30*24*time.Hour),
		JWTSecret:       getEnv("JWT_SECRET", "dev_secret_change_me"),
		CookieDomain:    getEnv("COOKIE_DOMAIN", ""),
		CookieSecure:    getEnvBool("COOKIE_SECURE", false),
		TrustedProxies:  splitAndClean(getEnv("TRUSTED_PROXIES", "")),
		TwoFAIssuer:     getEnv("TWOFA_ISSUER", "AtPost"),
		FrontendURL:     getEnv("FRONTEND_URL", "http://localhost:3000"),
		OAuth:           LoadOAuth(),
	}
	if cfg.JWTSecret == "" {
		log.Fatal("JWT_SECRET env var is required")
	}
	if cfg.JWTSecret == "dev_secret_change_me" {
		log.Println("WARNING: JWT_SECRET is set to the development default — do not use in production")
	}
	return cfg
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

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			return parsed
		}
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

func getEnvBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			return parsed
		}
	}
	return def
}

package config

import (
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
	JWTKID           string
	JWTSecretPrevious string
	JWTKIDPrevious    string
	AllowedOrigins   []string
	WSAllowQueryToken bool
	WSWriteWait      time.Duration
	WSPongWait       time.Duration
	WSPingPeriod     time.Duration
	WSMaxMessageSize int64
}

func Load() *Config {
	pongWait := getEnvDuration("WS_PONG_WAIT", 60*time.Second)
	pingPeriod := getEnvDuration("WS_PING_PERIOD", (pongWait*9)/10)
	return &Config{
		HTTPPort:         getEnv("HTTP_PORT", "8093"),
		HTTPReadTimeout:  getEnvDuration("HTTP_READ_TIMEOUT", 15*time.Second),
		HTTPWriteTimeout: getEnvDuration("HTTP_WRITE_TIMEOUT", 15*time.Second),
		HTTPIdleTimeout:  getEnvDuration("HTTP_IDLE_TIMEOUT", 60*time.Second),
		RedisAddr:        getEnv("REDIS_ADDR", "localhost:6379"),
		JWTSecret:        getEnv("JWT_SECRET", ""),
		JWTKID:            getEnv("JWT_KID", "v1"),
		JWTSecretPrevious: getEnv("JWT_SECRET_PREVIOUS", ""),
		JWTKIDPrevious:    getEnv("JWT_KID_PREVIOUS", ""),
		AllowedOrigins:   splitAndClean(getEnv("ALLOWED_ORIGINS", "")),
		WSAllowQueryToken: getEnvBool("WS_ALLOW_QUERY_TOKEN", true),
		WSWriteWait:      getEnvDuration("WS_WRITE_WAIT", 10*time.Second),
		WSPongWait:       pongWait,
		WSPingPeriod:     pingPeriod,
		WSMaxMessageSize: getEnvInt64("WS_MAX_MESSAGE_SIZE", 64*1024),
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

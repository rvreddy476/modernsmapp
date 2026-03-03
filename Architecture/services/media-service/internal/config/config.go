package config

import "os"

// Config holds the runtime configuration for the media service.
type Config struct {
	HTTPPort       string
	PostgresDSN    string
	JWTSecret      string
	MinioEndpoint  string
	MinioAccessKey string
	MinioSecretKey string
	MinioBucket    string
	MinioUseSSL    bool
	MinioPublicEndpoint string
	KafkaBrokers   string

	// Content safety scanning
	ScannerEnabled bool   // env: MEDIA_SCANNER_ENABLED (default false)

	// Observability
	OTLPEndpoint   string // env: OTEL_EXPORTER_OTLP_ENDPOINT (default "http://jaeger:4318")
}

// Load reads configuration from environment variables with defaults.
func Load() *Config {
	return &Config{
		HTTPPort:       getEnv("HTTP_PORT", "8087"),
		PostgresDSN:    os.Getenv("POSTGRES_DSN"),
		JWTSecret:      os.Getenv("JWT_SECRET"),
		MinioEndpoint:  getEnv("MINIO_ENDPOINT", "minio:9000"),
		MinioAccessKey: getEnv("MINIO_ACCESS_KEY", "minioadmin"),
		MinioSecretKey: getEnv("MINIO_SECRET_KEY", "minioadmin"),
		MinioBucket:    getEnv("MINIO_BUCKET", "media"),
		MinioUseSSL:    os.Getenv("MINIO_USE_SSL") == "true",
		MinioPublicEndpoint: os.Getenv("MINIO_PUBLIC_ENDPOINT"),
		KafkaBrokers:   getEnv("KAFKA_BROKERS", "kafka:9092"),

		ScannerEnabled: os.Getenv("MEDIA_SCANNER_ENABLED") == "true",
		OTLPEndpoint:   getEnv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://jaeger:4318"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

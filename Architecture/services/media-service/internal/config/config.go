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

	// Content safety scanning.
	//
	// Audit H8: production deployments must wire a real scanner
	// (Google SafeSearch / AWS Rekognition / on-prem). The default
	// `StubScanner` always returns "safe", so leaving the gate on
	// with stub configured is the same as leaving it off — except
	// it looks compliant in audit logs.
	//
	// Policy:
	//   ScannerEnabled=false                       → no scan, no gate (dev / behind-perimeter)
	//   ScannerEnabled=true  + real scanner        → scan + fail-closed on error
	//   ScannerEnabled=true  + stub + AllowStub=t  → stub passes; loud startup WARN
	//   ScannerEnabled=true  + stub + AllowStub=f  → REJECT every image upload
	//                                                  (refuses to silently pretend it's scanning)
	ScannerEnabled  bool // env: MEDIA_SCANNER_ENABLED (default false)
	ScannerAllowStub bool // env: MEDIA_SCANNER_ALLOW_STUB (default false; opt-in for dev/test)

	// Transcription (Module 1 P0-6 / P0-9).
	//
	// Empty or "none" means no provider is wired: caption/transcript
	// endpoints report status "unavailable" and never fabricate a
	// completed transcript. Voice posts still work — they just publish
	// without captions, and the safety gate falls back to the configured
	// VoiceSafetyRequired policy below.
	TranscriptProvider string // env: MEDIA_TRANSCRIPT_PROVIDER (default "")

	// VoiceSafetyRequired gates public distribution of voice posts on a
	// completed baseline safety pass (transcript + audio scan). Default
	// true per Codex P0-6: "public distribution remains pending until
	// baseline transcript/audio safety completes". Set false only in dev.
	VoiceSafetyRequired bool // env: MEDIA_VOICE_SAFETY_REQUIRED (default true)

	// VoiceSafetyBlocklist configures the transcript safety evaluator
	// (comma/newline separated terms). When empty, NO evaluator is
	// configured and every voice asset fails closed to manual review —
	// availability is never treated as safety (fixes-v2 / Codex P0-2).
	VoiceSafetyBlocklist string // env: MEDIA_VOICE_SAFETY_BLOCKLIST

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

		ScannerEnabled:   os.Getenv("MEDIA_SCANNER_ENABLED") == "true",
		ScannerAllowStub: os.Getenv("MEDIA_SCANNER_ALLOW_STUB") == "true",

		TranscriptProvider: os.Getenv("MEDIA_TRANSCRIPT_PROVIDER"),
		// Default-on: absent env var means the safety gate applies.
		VoiceSafetyRequired:  os.Getenv("MEDIA_VOICE_SAFETY_REQUIRED") != "false",
		VoiceSafetyBlocklist: os.Getenv("MEDIA_VOICE_SAFETY_BLOCKLIST"),

		OTLPEndpoint:     getEnv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://jaeger:4318"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

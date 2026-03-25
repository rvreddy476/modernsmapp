package transport

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func envString(key string) string {
	return strings.TrimSpace(os.Getenv(key))
}

func envBool(key string, def bool) bool {
	raw := envString(key)
	if raw == "" {
		return def
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return def
	}
	return parsed
}

func envInt(key string, def int) int {
	raw := envString(key)
	if raw == "" {
		return def
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return parsed
}

func envDuration(key string, def time.Duration) time.Duration {
	raw := envString(key)
	if raw == "" {
		return def
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return def
	}
	return parsed
}

func tlsConfigFromEnv(prefix string) (*tls.Config, error) {
	enabled := envBool(prefix+"_TLS_ENABLED", false)
	insecureSkipVerify := envBool(prefix+"_TLS_INSECURE_SKIP_VERIFY", false)
	serverName := envString(prefix + "_TLS_SERVER_NAME")
	caFile := envString(prefix + "_CA_CERT_FILE")
	clientCertFile := envString(prefix + "_CLIENT_CERT_FILE")
	clientKeyFile := envString(prefix + "_CLIENT_KEY_FILE")

	if !enabled && !insecureSkipVerify && serverName == "" && caFile == "" && clientCertFile == "" && clientKeyFile == "" {
		return nil, nil
	}

	cfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: insecureSkipVerify,
	}
	if serverName != "" {
		cfg.ServerName = serverName
	}

	if caFile != "" {
		caPEM, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read %s CA cert: %w", prefix, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("parse %s CA cert", prefix)
		}
		cfg.RootCAs = pool
	}

	if clientCertFile != "" || clientKeyFile != "" {
		if clientCertFile == "" || clientKeyFile == "" {
			return nil, fmt.Errorf("%s client cert and key must be configured together", prefix)
		}
		cert, err := tls.LoadX509KeyPair(clientCertFile, clientKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load %s client cert: %w", prefix, err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}

	return cfg, nil
}

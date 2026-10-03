package search

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/opensearch-project/opensearch-go/v2"
)

// Config is everything the OpenSearch client needs to reach the cluster.
//
// Dev runs a plain-HTTP OpenSearch container with security disabled, so
// URL alone is enough there. Amazon OpenSearch (production) is configured
// with fine-grained access control and an internal master user: every
// request must carry HTTP basic auth over TLS or the domain answers 401 and
// the index silently never fills. ConfigFromEnv refuses to produce a
// production config that would behave that way.
type Config struct {
	URL      string
	Username string
	Password string
	// InsecureSkipVerify disables TLS certificate verification. Only for a
	// self-signed dev cluster; never allowed in production.
	InsecureSkipVerify bool
}

// Environment variable names read by ConfigFromEnv.
const (
	EnvURL                = "OPENSEARCH_URL"
	EnvUsername           = "OPENSEARCH_USERNAME"
	EnvPassword           = "OPENSEARCH_PASSWORD"
	EnvInsecureSkipVerify = "OPENSEARCH_INSECURE_SKIP_VERIFY"

	// DefaultURL is the docker-compose dev address.
	DefaultURL = "http://opensearch:9200"
)

// ConfigFromEnv reads the OpenSearch settings from getenv (nil means
// os.Getenv). When production is true it fails closed: an https URL with
// no credentials, half a credential pair, or certificate verification
// switched off is an error rather than a warning, because each of those
// starts a search-service that cannot write to (or should not trust) the
// cluster it is pointed at.
//
// Outside production nothing is required beyond a parseable URL, so the
// no-auth dev cluster keeps working unchanged.
func ConfigFromEnv(getenv func(string) string, production bool) (Config, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	cfg := Config{
		URL:      strings.TrimSpace(getenv(EnvURL)),
		Username: strings.TrimSpace(getenv(EnvUsername)),
		Password: getenv(EnvPassword),
	}
	if cfg.URL == "" {
		cfg.URL = DefaultURL
	}

	if raw := strings.TrimSpace(getenv(EnvInsecureSkipVerify)); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return Config{}, fmt.Errorf("%s: %q is not a boolean", EnvInsecureSkipVerify, raw)
		}
		cfg.InsecureSkipVerify = v
	}

	u, err := url.Parse(cfg.URL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return Config{}, fmt.Errorf("%s: %q is not an absolute http(s) URL", EnvURL, cfg.URL)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return Config{}, fmt.Errorf("%s: unsupported scheme %q (want http or https)", EnvURL, u.Scheme)
	}

	// Half a credential pair is a misconfiguration in every environment:
	// the client would send "user:" and be rejected.
	if (cfg.Username == "") != (cfg.Password == "") {
		return Config{}, fmt.Errorf("%s and %s must be set together", EnvUsername, EnvPassword)
	}

	if production {
		if strings.EqualFold(u.Scheme, "https") && cfg.Username == "" {
			return Config{}, fmt.Errorf("production refuses to start: %s is https (%s) but %s/%s are not set; Amazon OpenSearch fine-grained access control requires the internal master user credentials",
				EnvURL, redactURL(u), EnvUsername, EnvPassword)
		}
		if cfg.InsecureSkipVerify {
			return Config{}, fmt.Errorf("production refuses to start: %s=true disables TLS certificate verification", EnvInsecureSkipVerify)
		}
	}
	return cfg, nil
}

// HasCredentials reports whether basic auth will be sent.
func (c Config) HasCredentials() bool { return c.Username != "" && c.Password != "" }

// clientConfig turns Config into the opensearch-go client configuration.
// Credentials go through the client's own Username/Password fields, which
// it applies as an Authorization: Basic header on every request.
func (c Config) clientConfig() opensearch.Config {
	oc := opensearch.Config{
		Addresses: []string{c.URL},
		Username:  c.Username,
		Password:  c.Password,
	}
	if c.InsecureSkipVerify {
		oc.Transport = &http.Transport{
			Proxy:           http.ProxyFromEnvironment,
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // dev-only, refused in production by ConfigFromEnv
		}
	}
	return oc
}

// IsProductionEnv mirrors the detection the gateway, auth-service and the
// other Architecture services use: APP_ENV, then ENVIRONMENT, then ENV;
// "prod" / "production" mean production and an explicit other value on a
// higher-priority variable wins.
func IsProductionEnv() bool {
	for _, key := range []string{"APP_ENV", "ENVIRONMENT", "ENV"} {
		switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
		case "production", "prod":
			return true
		case "":
			continue
		default:
			return false
		}
	}
	return false
}

// redactURL strips any userinfo so an error message never echoes a
// password that was pasted into the URL.
func redactURL(u *url.URL) string {
	cp := *u
	cp.User = nil
	return cp.String()
}

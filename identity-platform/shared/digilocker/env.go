package digilocker

import (
	"fmt"
	"net/url"
	"strings"
)

// Environment variables. The names match the per-service copies, so one
// deployment secret can serve every DigiLocker caller.
const (
	EnvMode         = "DIGILOCKER_MODE"
	EnvClientID     = "DIGILOCKER_CLIENT_ID"
	EnvClientSecret = "DIGILOCKER_CLIENT_SECRET" //nolint:gosec // G101: name of the env variable, not its value
	EnvAuthorizeURL = "DIGILOCKER_AUTHORIZE_URL"
	EnvTokenURL     = "DIGILOCKER_TOKEN_URL"
	EnvEAadhaarURL  = "DIGILOCKER_EAADHAAR_URL"
)

// Settings is the DigiLocker wiring a service runs with.
type Settings struct {
	Mode         string
	Production   bool
	Client       Client
	AuthorizeURL string
	ClientID     string
	RedirectURI  string
}

// Mock reports whether the mock provider is selected.
func (s Settings) Mock() bool { return s.Mode == ModeMock && s.Client != nil }

// SettingsFromEnv reads the DigiLocker configuration. redirectURI is the
// caller's registered return URL. An unset DIGILOCKER_MODE is "disabled" in
// production and "mock" elsewhere; production URLs must be https; the
// default MeriPehchaan endpoints are used when a URL is unset.
func SettingsFromEnv(getenv func(string) string, production bool, redirectURI string) (Settings, error) {
	mode := normalizeMode(getenv(EnvMode))
	if mode == "" {
		mode = ModeMock
		if production {
			mode = ModeDisabled
		}
	}
	s := Settings{
		Mode:         mode,
		Production:   production,
		AuthorizeURL: orDefault(getenv(EnvAuthorizeURL), DefaultAuthorizeURL),
		ClientID:     strings.TrimSpace(getenv(EnvClientID)),
		RedirectURI:  strings.TrimSpace(redirectURI),
	}
	cfg := HTTPConfig{
		TokenURL:     orDefault(getenv(EnvTokenURL), DefaultTokenURL),
		EAadhaarURL:  orDefault(getenv(EnvEAadhaarURL), DefaultEAadhaarURL),
		ClientID:     s.ClientID,
		ClientSecret: strings.TrimSpace(getenv(EnvClientSecret)),
		RedirectURI:  s.RedirectURI,
	}
	for _, u := range []struct{ name, value string }{
		{EnvAuthorizeURL, s.AuthorizeURL}, {EnvTokenURL, cfg.TokenURL}, {EnvEAadhaarURL, cfg.EAadhaarURL}, {"redirect URI", s.RedirectURI},
	} {
		if err := CheckURL(u.name, u.value, production); err != nil {
			return Settings{}, err
		}
	}
	client, err := New(mode, production, cfg)
	if err != nil {
		return Settings{}, err
	}
	s.Client = client
	return s, nil
}

func orDefault(v, def string) string {
	if v = strings.TrimSpace(v); v != "" {
		return v
	}
	return def
}

// CheckURL names the variable, never its value. An empty value passes.
func CheckURL(name, value string, production bool) error {
	if value == "" {
		return nil
	}
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("%s must be an absolute http(s) URL", name)
	}
	if production && u.Scheme != "https" {
		return fmt.Errorf("%s must be https unless the environment is local, dev or development", name)
	}
	return nil
}

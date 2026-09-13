package digilocker

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/atpost/food-service/internal/foodpii"
)

const (
	EnvMode               = "DIGILOCKER_MODE"
	EnvClientID           = "DIGILOCKER_CLIENT_ID"
	EnvClientSecret       = "DIGILOCKER_CLIENT_SECRET"
	EnvAuthorizeURL       = "DIGILOCKER_AUTHORIZE_URL"
	EnvTokenURL           = "DIGILOCKER_TOKEN_URL"
	EnvIssuedDocumentsURL = "DIGILOCKER_ISSUED_DOCUMENTS_URL"
	// EnvPublicBaseURL is the public HTTPS origin the gateway serves
	// /v1/food on. The OAuth redirect_uri is this plus ReturnPath.
	EnvPublicBaseURL = "FOOD_PUBLIC_BASE_URL"
	// EnvRiderAppLinkURL is the Rider App Link the return route 302s to.
	EnvRiderAppLinkURL = "FOOD_RIDER_APP_LINK_URL"

	// ReturnPath is the public, unauthenticated OAuth return route.
	ReturnPath = "/v1/food/public/digilocker/return"
	// DevAuthorizePath is the mock provider's authorize page (local/dev only).
	DevAuthorizePath = "/v1/food/dev/digilocker/authorize"
)

// Settings is the DigiLocker wiring food-service runs with.
type Settings struct {
	Mode         string
	Production   bool
	Client       Client
	AuthorizeURL string
	ClientID     string
	// PublicBaseURL has no trailing slash.
	PublicBaseURL string
	AppLinkURL    string
}

// Mock reports whether the mock provider is selected.
func (s Settings) Mock() bool { return s.Mode == ModeMock && s.Client != nil }

// RedirectURI is the redirect_uri registered with DigiLocker, or "" when the
// public base URL is not configured.
func (s Settings) RedirectURI() string {
	if s.PublicBaseURL == "" {
		return ""
	}
	return s.PublicBaseURL + ReturnPath
}

// SettingsFromEnv reads the DigiLocker configuration. An unset
// DIGILOCKER_MODE means mock in local/dev and is an error in production; a
// URL that does not parse as absolute is an error; production URLs must be
// https.
func SettingsFromEnv(getenv func(string) string) (Settings, error) {
	production := foodpii.IsProduction(getenv("ENV"))
	mode := normalizeMode(getenv(EnvMode))
	if mode == "" {
		if production {
			return Settings{}, fmt.Errorf("%s is required unless ENV is local, dev or development (http, or disabled)", EnvMode)
		}
		mode = ModeMock
	}
	s := Settings{
		Mode:          mode,
		Production:    production,
		AuthorizeURL:  strings.TrimSpace(getenv(EnvAuthorizeURL)),
		ClientID:      strings.TrimSpace(getenv(EnvClientID)),
		PublicBaseURL: strings.TrimRight(strings.TrimSpace(getenv(EnvPublicBaseURL)), "/"),
		AppLinkURL:    strings.TrimSpace(getenv(EnvRiderAppLinkURL)),
	}
	cfg := HTTPConfig{
		TokenURL:           strings.TrimSpace(getenv(EnvTokenURL)),
		IssuedDocumentsURL: strings.TrimSpace(getenv(EnvIssuedDocumentsURL)),
		ClientID:           s.ClientID,
		ClientSecret:       strings.TrimSpace(getenv(EnvClientSecret)),
		RedirectURI:        s.RedirectURI(),
	}
	for _, u := range []struct{ name, value string }{
		{EnvPublicBaseURL, s.PublicBaseURL}, {EnvRiderAppLinkURL, s.AppLinkURL}, {EnvAuthorizeURL, s.AuthorizeURL},
		{EnvTokenURL, cfg.TokenURL}, {EnvIssuedDocumentsURL, cfg.IssuedDocumentsURL},
	} {
		if err := checkURL(u.name, u.value, production); err != nil {
			return Settings{}, err
		}
	}
	if mode == ModeHTTP && s.AuthorizeURL == "" {
		return Settings{}, fmt.Errorf("digilocker: DIGILOCKER_MODE=http needs %s", EnvAuthorizeURL)
	}
	client, err := New(mode, production, cfg)
	if err != nil {
		return Settings{}, err
	}
	s.Client = client
	return s, nil
}

// checkURL names the variable, never its value.
func checkURL(name, value string, production bool) error {
	if value == "" {
		return nil
	}
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("%s must be an absolute http(s) URL", name)
	}
	if production && u.Scheme != "https" {
		return fmt.Errorf("%s must be https unless ENV is local, dev or development", name)
	}
	return nil
}

package actionlinks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Safety check (Google Web Risk Lookup API).
//
//	GET https://webrisk.googleapis.com/v1/uris:search
//	    ?uri=<url>&threatTypes=MALWARE&threatTypes=SOCIAL_ENGINEERING
//	    &threatTypes=UNWANTED_SOFTWARE
//	X-Goog-Api-Key: <WEB_RISK_API_KEY>
//
//	200 {}                                         no match: safe
//	200 {"threat":{"threatTypes":[...],"expireTime":...}}  unsafe
//
// The key travels in the X-Goog-Api-Key header, never the query string, so
// it cannot land in a proxy or access log; no error this file returns
// quotes a request, a response body or the key.

// EnvWebRiskAPIKey names the key. Unset: no checker is built (see
// FromEnv); the service then allows links as "unchecked" outside
// production and refuses them (503) in production.
const EnvWebRiskAPIKey = "WEB_RISK_API_KEY" //nolint:gosec // G101: name of the env variable, not its value

// EnvWebRiskMock turns on the mock checker when no key is set (dev stacks
// only; production ignores it, so a production deployment can never run
// on the mock).
const EnvWebRiskMock = "WEB_RISK_MOCK"

// DefaultWebRiskURL is the Lookup API endpoint.
const DefaultWebRiskURL = "https://webrisk.googleapis.com/v1/uris:search"

// WebRiskTimeout bounds one lookup.
const WebRiskTimeout = 3 * time.Second

// ThreatTypes are the lists every lookup asks about.
var ThreatTypes = []string{"MALWARE", "SOCIAL_ENGINEERING", "UNWANTED_SOFTWARE"}

// ErrCheckUnavailable: the checker could not give a verdict (transport
// failure, timeout, non-200, a body outside the contract). Callers fail
// closed: 503 ACTION_LINK_CHECK_UNAVAILABLE on a write, "try again later"
// in the recheck worker.
var ErrCheckUnavailable = errors.New("the link safety check is unavailable")

// Verdict is one lookup's answer.
type Verdict struct {
	Unsafe      bool
	ThreatTypes []string
}

// Checker gives a safety verdict for one URL.
type Checker interface {
	Check(ctx context.Context, rawURL string) (Verdict, error)
	// Name is "google_web_risk" or "mock", for logs.
	Name() string
}

// GoogleWebRisk is the Lookup API client.
type GoogleWebRisk struct {
	apiKey   string
	endpoint string
	http     *http.Client
}

// NewGoogleWebRisk builds the client. endpoint "" is DefaultWebRiskURL;
// client nil gets WebRiskTimeout and no redirects (a redirect would resend
// the key header somewhere else).
func NewGoogleWebRisk(apiKey, endpoint string, client *http.Client) *GoogleWebRisk {
	if endpoint == "" {
		endpoint = DefaultWebRiskURL
	}
	if client == nil {
		client = &http.Client{
			Timeout:       WebRiskTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	return &GoogleWebRisk{apiKey: apiKey, endpoint: endpoint, http: client}
}

func (g *GoogleWebRisk) Name() string { return "google_web_risk" }

// Check asks the Lookup API about rawURL.
func (g *GoogleWebRisk) Check(ctx context.Context, rawURL string) (Verdict, error) {
	q := url.Values{}
	q.Set("uri", rawURL)
	for _, t := range ThreatTypes {
		q.Add("threatTypes", t)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return Verdict{}, ErrCheckUnavailable
	}
	req.Header.Set("X-Goog-Api-Key", g.apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := g.http.Do(req)
	if err != nil {
		return Verdict{}, fmt.Errorf("%w: request failed", ErrCheckUnavailable)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return Verdict{}, fmt.Errorf("%w: unreadable answer", ErrCheckUnavailable)
	}
	if resp.StatusCode != http.StatusOK {
		return Verdict{}, fmt.Errorf("%w: status %d", ErrCheckUnavailable, resp.StatusCode)
	}
	return parseWebRiskAnswer(body)
}

// parseWebRiskAnswer reads a 200 body: an object, with "threat" present
// exactly when the URL is on a list.
func parseWebRiskAnswer(body []byte) (Verdict, error) {
	var w struct {
		Threat *struct {
			ThreatTypes []string `json:"threatTypes"`
		} `json:"threat"`
	}
	trimmed := strings.TrimSpace(string(body))
	if !strings.HasPrefix(trimmed, "{") {
		return Verdict{}, fmt.Errorf("%w: answer is not an object", ErrCheckUnavailable)
	}
	if err := json.Unmarshal(body, &w); err != nil {
		return Verdict{}, fmt.Errorf("%w: malformed answer", ErrCheckUnavailable)
	}
	if w.Threat == nil {
		return Verdict{}, nil
	}
	return Verdict{Unsafe: true, ThreatTypes: w.Threat.ThreatTypes}, nil
}

// Mock is the dev / test checker. Unsafe decides each URL; nil means
// MockUnsafe (Google's own Safe Browsing test pages).
type Mock struct {
	Unsafe func(rawURL string) bool
	// Err, when set, is returned by every Check (an outage).
	Err error
}

func (m *Mock) Name() string { return "mock" }

func (m *Mock) Check(_ context.Context, rawURL string) (Verdict, error) {
	if m.Err != nil {
		return Verdict{}, m.Err
	}
	unsafe := m.Unsafe
	if unsafe == nil {
		unsafe = MockUnsafe
	}
	if unsafe(rawURL) {
		return Verdict{Unsafe: true, ThreatTypes: []string{"SOCIAL_ENGINEERING"}}, nil
	}
	return Verdict{}, nil
}

// MockUnsafe flags Google's published Safe Browsing test pages
// (testsafebrowsing.appspot.com/s/malware.html, /s/phishing.html,
// /s/unwanted.html), so a dev stack can exercise the unsafe path with the
// same URLs a real key would flag.
func MockUnsafe(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	if !strings.EqualFold(u.Hostname(), "testsafebrowsing.appspot.com") {
		return false
	}
	p := strings.ToLower(u.Path)
	return strings.HasPrefix(p, "/s/malware") || strings.HasPrefix(p, "/s/phishing") || strings.HasPrefix(p, "/s/unwanted")
}

// CheckerFromEnv builds the checker: Google Web Risk with WEB_RISK_API_KEY;
// otherwise, outside production, the mock when WEB_RISK_MOCK=true; otherwise
// nil (no checker). production never gets the mock.
func CheckerFromEnv(getenv func(string) string, production bool) Checker {
	if key := strings.TrimSpace(getenv(EnvWebRiskAPIKey)); key != "" {
		return NewGoogleWebRisk(key, "", nil)
	}
	if !production && strings.EqualFold(strings.TrimSpace(getenv(EnvWebRiskMock)), "true") {
		return &Mock{}
	}
	return nil
}

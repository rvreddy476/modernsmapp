package service

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/sync/singleflight"
)

// Mobile OAuth sign-in (POST /v1/auth/oauth/:provider/token).
//
// The route used to take a bare provider ACCESS token and call the provider's
// userinfo endpoint with it. Nothing tied that token to this app: an access
// token a victim granted to ANY other OAuth client (a quiz site, a browser
// extension) opened their account here, and auto-linked a verified local
// account with the same email. Only an ID token says who it was issued to, in
// a claim the provider signs (aud), so that is now the only credential the
// route accepts:
//
//   - Google: an OpenID Connect ID token, verified against Google's published
//     keys, with aud one of OAUTH_GOOGLE_ID_TOKEN_AUDIENCES (or the web client
//     id), iss Google, exp in the future and email_verified true.
//   - GitHub issues no ID token, and Apple's needs its own verifier; both are
//     refused on this route. They remain available through the browser flow,
//     where the code exchange itself proves the token is ours.
//
// No audiences configured means the route is closed, never open.

var (
	// ErrOAuthTokenProviderUnsupported: the provider has no verifiable ID
	// token on this route.
	ErrOAuthTokenProviderUnsupported = errors.New("this provider cannot sign in with a token; use the browser sign-in")
	// ErrOAuthNotConfigured: the provider has no client configured here.
	ErrOAuthNotConfigured = errors.New("sign-in with this provider is not configured")
	// ErrOAuthIDTokenInvalid: signature, issuer, audience, expiry or the
	// email assertion did not check out.
	ErrOAuthIDTokenInvalid = errors.New("the provider ID token is not valid for this app")
)

const (
	googleJWKSURL       = "https://www.googleapis.com/oauth2/v3/certs"
	googleKeysMinTTL    = 5 * time.Minute
	googleKeysMaxTTL    = 24 * time.Hour
	googleKeysDefault   = time.Hour
	googleRefetchMinGap = time.Minute
	idTokenClockSkew    = 60 * time.Second
	oauthHTTPTimeout    = 10 * time.Second
)

var googleIssuers = map[string]bool{"https://accounts.google.com": true, "accounts.google.com": true}

// googleIDClaims is the subset of a Google ID token this service reads.
type googleIDClaims struct {
	jwt.RegisteredClaims
	Email         string          `json:"email"`
	EmailVerified json.RawMessage `json:"email_verified"`
	Name          string          `json:"name"`
}

// googleKeySet fetches and caches Google's ID-token signing keys.
type googleKeySet struct {
	url    string
	client *http.Client
	now    func() time.Time

	// mu guards the cache only; it is never held across the HTTP fetch, so
	// a slow Google response cannot stall sign-ins the cache can answer.
	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	expires   time.Time
	lastFetch time.Time
	// fetches collapses concurrent refreshes into one request.
	fetches singleflight.Group
}

func newGoogleKeySet() *googleKeySet {
	return &googleKeySet{url: googleJWKSURL, client: &http.Client{Timeout: oauthHTTPTimeout}, now: time.Now}
}

// key returns the public key for kid, refreshing the cache when it is stale
// or does not know kid (Google rotates keys), at most once a minute. Callers
// that need a refresh share one fetch and wait for it, each only as long as
// its own context allows.
func (g *googleKeySet) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	g.mu.Lock()
	now := g.now()
	k, known := g.keys[kid]
	fresh := known && now.Before(g.expires)
	stale := g.keys == nil || now.After(g.expires) || now.Sub(g.lastFetch) >= googleRefetchMinGap
	g.mu.Unlock()
	if fresh {
		return k, nil
	}
	if stale {
		// The fetch outlives a caller that gives up: the others share it.
		done := g.fetches.DoChan("keys", func() (any, error) {
			return nil, g.refresh(context.WithoutCancel(ctx))
		})
		select {
		case res := <-done:
			if res.Err != nil {
				return nil, res.Err
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if k, ok := g.keys[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("unknown google signing key %q", kid)
}

// refresh fetches the key set and installs it, unless a fetch that finished
// while this caller was deciding has just done so.
func (g *googleKeySet) refresh(ctx context.Context) error {
	g.mu.Lock()
	now := g.now()
	if g.keys != nil && now.Before(g.expires) && now.Sub(g.lastFetch) < googleRefetchMinGap {
		g.mu.Unlock()
		return nil
	}
	g.lastFetch = now
	g.mu.Unlock()

	keys, ttl, err := g.fetch(ctx)
	if err != nil {
		return err
	}
	g.mu.Lock()
	g.keys = keys
	g.expires = g.now().Add(ttl)
	g.mu.Unlock()
	return nil
}

// fetch downloads and parses Google's key set. It touches no cache state.
func (g *googleKeySet) fetch(ctx context.Context) (map[string]*rsa.PublicKey, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.url, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("fetch google keys: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("fetch google keys: status %d", resp.StatusCode)
	}
	var set struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&set); err != nil {
		return nil, 0, fmt.Errorf("decode google keys: %w", err)
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		if k.Kty != "RSA" || k.Kid == "" {
			continue
		}
		pub, err := rsaKeyFromJWK(k.N, k.E)
		if err != nil {
			continue
		}
		keys[k.Kid] = pub
	}
	if len(keys) == 0 {
		return nil, 0, errors.New("google key set has no usable RSA keys")
	}
	return keys, cacheTTL(resp.Header.Get("Cache-Control")), nil
}

func rsaKeyFromJWK(n, e string) (*rsa.PublicKey, error) {
	nb, err := base64.RawURLEncoding.DecodeString(n)
	if err != nil {
		return nil, err
	}
	eb, err := base64.RawURLEncoding.DecodeString(e)
	if err != nil {
		return nil, err
	}
	exp := new(big.Int).SetBytes(eb)
	if !exp.IsInt64() || exp.Int64() < 3 || len(nb) < 256 {
		return nil, errors.New("weak or malformed RSA key")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nb), E: int(exp.Int64())}, nil
}

// cacheTTL reads max-age from Cache-Control, clamped to a sane range.
func cacheTTL(header string) time.Duration {
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if v, ok := strings.CutPrefix(part, "max-age="); ok {
			if secs, err := strconv.Atoi(v); err == nil {
				d := time.Duration(secs) * time.Second
				if d < googleKeysMinTTL {
					return googleKeysMinTTL
				}
				if d > googleKeysMaxTTL {
					return googleKeysMaxTTL
				}
				return d
			}
		}
	}
	return googleKeysDefault
}

// verifyGoogleIDToken checks raw against Google's keys and returns the
// asserted identity. audiences must be non-empty.
func verifyGoogleIDToken(ctx context.Context, keys *googleKeySet, raw string, audiences []string, now time.Time) (*OAuthUserInfo, error) {
	if len(audiences) == 0 {
		return nil, ErrOAuthNotConfigured
	}
	claims := &googleIDClaims{}
	_, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (interface{}, error) {
		kid, _ := t.Header["kid"].(string)
		return keys.key(ctx, kid)
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(idTokenClockSkew),
		jwt.WithTimeFunc(func() time.Time { return now }),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOAuthIDTokenInvalid, err)
	}
	if !googleIssuers[claims.Issuer] {
		return nil, fmt.Errorf("%w: issuer", ErrOAuthIDTokenInvalid)
	}
	if !audienceAllowed(claims.Audience, audiences) {
		return nil, fmt.Errorf("%w: audience", ErrOAuthIDTokenInvalid)
	}
	if claims.Subject == "" || strings.TrimSpace(claims.Email) == "" {
		return nil, fmt.Errorf("%w: subject or email missing", ErrOAuthIDTokenInvalid)
	}
	if !appleBoolClaim(claims.EmailVerified) {
		return nil, fmt.Errorf("%w: email not verified by provider", ErrOAuthIDTokenInvalid)
	}
	return &OAuthUserInfo{Email: claims.Email, Name: claims.Name, Sub: claims.Subject, EmailVerified: true}, nil
}

func audienceAllowed(got jwt.ClaimStrings, allowed []string) bool {
	for _, a := range got {
		for _, want := range allowed {
			if want != "" && a == want {
				return true
			}
		}
	}
	return false
}

// googleIDTokenAudiences is every client id a Google ID token may be issued
// to: the installed-app (Android) client ids and the web client id.
func (s *Service) googleIDTokenAudiences() []string {
	if s.cfg.OAuth == nil {
		return nil
	}
	var out []string
	for _, a := range append(append([]string(nil), s.cfg.OAuth.Google.IDTokenAudiences...), s.cfg.OAuth.Google.ClientID) {
		if a = strings.TrimSpace(a); a != "" {
			out = append(out, a)
		}
	}
	return out
}

// googleKeys returns the service's Google key cache, creating it once.
func (s *Service) googleKeys() *googleKeySet {
	s.googleKeysOnce.Do(func() {
		if s.googleKeySet == nil {
			s.googleKeySet = newGoogleKeySet()
		}
	})
	return s.googleKeySet
}

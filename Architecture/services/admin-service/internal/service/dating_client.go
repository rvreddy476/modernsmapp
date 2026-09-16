package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/atpost/shared/o11y/trace"
	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

// The service-token contract admin-service uses with every product service.
// A product verifies the token, not where the request came from:
//
//	iss   admin-service               (TokenIssuer)
//	aud   the product, e.g. "dating"
//	scope [the ONE permission the gate checked for this route]
//	act   the acting admin's user id
//	exp   ProductTokenTTL
//
// No internal key and no actor header is sent: the key is stamped on edge
// traffic by the gateway, and a header can be set by anyone holding the key.
const (
	TokenIssuer     = "admin-service"
	ProductTokenTTL = 60 * time.Second
	// DatingAudience is the audience dating-service verifies.
	DatingAudience = "dating"
	// DatingAdminPrefix is dating's token-only admin family.
	DatingAdminPrefix = "/v1/dating/internal/admin"
	// ServiceAuthHeader carries the token.
	ServiceAuthHeader = "X-Service-Authorization"
	// IdempotencyKeyHeader is forwarded verbatim when the console sends one.
	IdempotencyKeyHeader = "Idempotency-Key"
)

// ErrProductUnavailable: the signing key is not configured, so no product
// call can be authorised. The handler answers 503 rather than calling out.
var ErrProductUnavailable = errors.New("admin-service has no service-token key for this product")

// ErrActorRequired is returned, before any request is sent, when a product
// call has no valid acting admin: the call would be attributed to nobody.
var ErrActorRequired = errors.New("product admin call requires the acting admin's user id")

// SignerFromEnv loads admin-service's own Ed25519 key:
//
//	ADMIN_SERVICE_TOKEN_KEY  base64 32-byte seed or 64-byte private key
//	ADMIN_SERVICE_TOKEN_KID  key id products registered it under
//
// No key → (nil, nil): product dashboards answer 503 and boot continues. A
// key without a kid, or an unreadable key, is a configuration error.
func SignerFromEnv(getenv func(string) string) (*servicetoken.Signer, error) {
	key := strings.TrimSpace(getenv("ADMIN_SERVICE_TOKEN_KEY"))
	kid := strings.TrimSpace(getenv("ADMIN_SERVICE_TOKEN_KID"))
	if key == "" {
		return nil, nil
	}
	if kid == "" {
		return nil, errors.New("ADMIN_SERVICE_TOKEN_KEY is set but ADMIN_SERVICE_TOKEN_KID is empty")
	}
	s, err := servicetoken.NewSignerFromBase64(TokenIssuer, kid, key)
	if err != nil {
		return nil, fmt.Errorf("ADMIN_SERVICE_TOKEN_KEY: %w", err)
	}
	return s, nil
}

// ProductClient calls one product service's token-only admin family as a
// human admin. Every product gets one, with its audience and prefix.
type ProductClient struct {
	baseURL    string
	prefix     string
	audience   string
	signer     *servicetoken.Signer
	httpClient *http.Client
}

func newProductClient(baseURL, prefix, audience string, signer *servicetoken.Signer) *ProductClient {
	return &ProductClient{
		baseURL:  strings.TrimRight(baseURL, "/"),
		prefix:   prefix,
		audience: audience,
		signer:   signer,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
			// A redirect is never followed server-side: a presigned object-store
			// URL is handed back to the console (ProductResponse.Location), and
			// the token is never replayed to another host.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// NewDatingClient builds the client for dating-service. A nil signer yields a
// client whose every call returns ErrProductUnavailable.
func NewDatingClient(baseURL string, signer *servicetoken.Signer) *ProductClient {
	return newProductClient(baseURL, DatingAdminPrefix, DatingAudience, signer)
}

// Audience is the audience this client signs for.
func (p *ProductClient) Audience() string {
	if p == nil {
		return ""
	}
	return p.audience
}

// ProductRequest is one admin call. Path is relative to the product's admin
// prefix; Permission becomes the token's only scope; Actor its act claim.
type ProductRequest struct {
	Method     string
	Path       string
	Query      url.Values
	RawQuery   string // used when Query is empty
	Permission string
	Actor      string
	// Body is JSON-encoded when non-nil; RawBody (already JSON) wins over it.
	Body    any
	RawBody []byte
	// IdempotencyKey is sent as Idempotency-Key when set.
	IdempotencyKey string
}

// ProductResponse is what the product answered. Location is set on a 3xx.
type ProductResponse struct {
	Status             int
	Body               []byte
	ContentType        string
	ContentDisposition string
	Location           string
}

// Call performs one admin operation with a JSON body (or none).
func (p *ProductClient) Call(ctx context.Context, method, path string, query url.Values, permission, actor string, body any) ([]byte, int, error) {
	resp, err := p.Do(ctx, ProductRequest{Method: method, Path: path, Query: query, Permission: permission, Actor: actor, Body: body})
	return resp.Body, resp.Status, err
}

// Do performs one admin operation.
func (p *ProductClient) Do(ctx context.Context, r ProductRequest) (ProductResponse, error) {
	if p == nil || p.signer == nil {
		return ProductResponse{}, ErrProductUnavailable
	}
	id, err := uuid.Parse(r.Actor)
	if err != nil || id == uuid.Nil {
		return ProductResponse{}, ErrActorRequired
	}
	if r.Permission == "" {
		return ProductResponse{}, errors.New("product call without a permission")
	}
	tok, err := p.signer.Mint(p.audience, "admin-console", []string{r.Permission}, nil, ProductTokenTTL,
		servicetoken.WithActor(id.String()))
	if err != nil {
		return ProductResponse{}, fmt.Errorf("mint service token: %w", err)
	}

	target := p.baseURL + p.prefix + r.Path
	if len(r.Query) > 0 {
		target += "?" + r.Query.Encode()
	} else if r.RawQuery != "" {
		target += "?" + r.RawQuery
	}
	var rd io.Reader
	hasBody := false
	switch {
	case r.RawBody != nil:
		rd, hasBody = bytes.NewReader(r.RawBody), true
	case r.Body != nil:
		b, err := json.Marshal(r.Body)
		if err != nil {
			return ProductResponse{}, fmt.Errorf("marshal: %w", err)
		}
		rd, hasBody = bytes.NewReader(b), true
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, target, rd)
	if err != nil {
		return ProductResponse{}, fmt.Errorf("new request: %w", err)
	}
	if hasBody {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set(ServiceAuthHeader, "Bearer "+tok)
	if r.IdempotencyKey != "" {
		req.Header.Set(IdempotencyKeyHeader, r.IdempotencyKey)
	}
	if rid := trace.RequestIDFrom(ctx); rid != "" {
		req.Header.Set(trace.HeaderRequestID, rid)
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return ProductResponse{}, fmt.Errorf("%s admin call: %w", p.audience, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	out := ProductResponse{
		Status:             resp.StatusCode,
		Body:               data,
		ContentType:        resp.Header.Get("Content-Type"),
		ContentDisposition: resp.Header.Get("Content-Disposition"),
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		out.Location = resp.Header.Get("Location")
	}
	if err != nil {
		return out, fmt.Errorf("read %s response: %w", p.audience, err)
	}
	return out, nil
}

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
)

// ErrProductUnavailable: the signing key is not configured, so no product
// call can be authorised. The handler answers 503 rather than calling out.
var ErrProductUnavailable = errors.New("admin-service has no service-token key for this product")

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
// human admin. Feast, MStore and the rest get one each, with their audience.
type ProductClient struct {
	baseURL    string
	prefix     string
	audience   string
	signer     *servicetoken.Signer
	httpClient *http.Client
}

// NewDatingClient builds the client for dating-service. A nil signer yields a
// client whose every call returns ErrProductUnavailable.
func NewDatingClient(baseURL string, signer *servicetoken.Signer) *ProductClient {
	return &ProductClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		prefix:     DatingAdminPrefix,
		audience:   DatingAudience,
		signer:     signer,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// Call performs one admin operation. permission is the permission the gate
// checked for this route and becomes the token's only scope; actor is the
// gate's admitted admin. path is relative to the product's admin prefix.
func (p *ProductClient) Call(ctx context.Context, method, path string, query url.Values, permission, actor string, body any) ([]byte, int, error) {
	if p == nil || p.signer == nil {
		return nil, 0, ErrProductUnavailable
	}
	id, err := uuid.Parse(actor)
	if err != nil || id == uuid.Nil {
		return nil, 0, ErrActorRequired
	}
	if permission == "" {
		return nil, 0, errors.New("product call without a permission")
	}
	tok, err := p.signer.Mint(p.audience, "admin-console", []string{permission}, nil, ProductTokenTTL,
		servicetoken.WithActor(id.String()))
	if err != nil {
		return nil, 0, fmt.Errorf("mint service token: %w", err)
	}

	target := p.baseURL + p.prefix + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("marshal: %w", err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, rd)
	if err != nil {
		return nil, 0, fmt.Errorf("new request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set(ServiceAuthHeader, "Bearer "+tok)
	if rid := trace.RequestIDFrom(ctx); rid != "" {
		req.Header.Set(trace.HeaderRequestID, rid)
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%s admin call: %w", p.audience, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read %s response: %w", p.audience, err)
	}
	return data, resp.StatusCode, nil
}

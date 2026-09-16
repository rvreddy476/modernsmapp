package adminauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Permissions is one admin's resolved permission map, exactly as identity
// returns it: Apps holds "<app>:<resource>.<action>" keyed by app, Platform
// holds "platform:..." and "*:..." permissions.
type Permissions struct {
	UserID   string              `json:"user_id"`
	Apps     map[string][]string `json:"apps"`
	Platform []string            `json:"platform"`
}

// Has reports whether the map holds perm exactly. An app-scoped grant only
// ever appears under its own app, so a permission for one app never satisfies
// another app's route.
func (p Permissions) Has(perm string) bool {
	if perm == "" {
		return false
	}
	for _, x := range p.Platform {
		if x == perm {
			return true
		}
	}
	for _, x := range p.Apps[AppOf(perm)] {
		if x == perm {
			return true
		}
	}
	return false
}

// All returns every permission held, sorted.
func (p Permissions) All() []string {
	out := append([]string(nil), p.Platform...)
	for _, perms := range p.Apps {
		out = append(out, perms...)
	}
	sort.Strings(out)
	return out
}

// AppOf is the app part of "<app>:<resource>.<action>". "*" (cross-app audit)
// and "platform" both belong to the platform.
func AppOf(perm string) string {
	i := strings.IndexByte(perm, ':')
	if i <= 0 {
		return ""
	}
	if a := perm[:i]; a != "*" {
		return a
	}
	return "platform"
}

// ValidPermission reports whether perm has the "<app>:<resource>.<action>" shape.
func ValidPermission(perm string) bool {
	i := strings.IndexByte(perm, ':')
	if i <= 0 || i == len(perm)-1 {
		return false
	}
	rest := perm[i+1:]
	j := strings.IndexByte(rest, '.')
	return j > 0 && j < len(rest)-1 && !strings.ContainsAny(perm, " /")
}

// ErrIdentityUnavailable wraps every failure to get an answer from identity.
// Callers enforcing access treat it as a refusal.
var ErrIdentityUnavailable = errors.New("identity could not answer")

// IdentityClient calls auth-service's service-only routes. It sends the
// internal key and nothing else: auth-service refuses any request carrying an
// end-user identity header (X-User-Id, X-Verified-User-Id, X-Scopes,
// X-Admin-Role), so none is ever set here.
type IdentityClient struct {
	baseURL     string
	internalKey string
	httpClient  *http.Client
}

func NewIdentityClient(baseURL, internalKey string) *IdentityClient {
	return &IdentityClient{
		baseURL:     strings.TrimRight(baseURL, "/"),
		internalKey: internalKey,
		httpClient:  &http.Client{Timeout: 3 * time.Second},
	}
}

func (c *IdentityClient) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrIdentityUnavailable, err)
	}
	req.Header.Set("X-Internal-Service-Key", c.internalKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrIdentityUnavailable, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%w: read: %v", ErrIdentityUnavailable, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: status %d", ErrIdentityUnavailable, resp.StatusCode)
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil || len(env.Data) == 0 || string(env.Data) == "null" {
		return fmt.Errorf("%w: malformed response", ErrIdentityUnavailable)
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("%w: malformed data: %v", ErrIdentityUnavailable, err)
	}
	return nil
}

// UserPermissions — GET /v1/auth/internal/users/:userId/permissions
//
//	200 {"data":{"user_id":"<uuid>","admin":{"apps":{...},"platform":[...]}}}
func (c *IdentityClient) UserPermissions(ctx context.Context, userID string) (Permissions, error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return Permissions{}, fmt.Errorf("%w: invalid user id", ErrIdentityUnavailable)
	}
	var data struct {
		UserID string `json:"user_id"`
		Admin  *struct {
			Apps     map[string][]string `json:"apps"`
			Platform []string            `json:"platform"`
		} `json:"admin"`
	}
	if err := c.get(ctx, "/v1/auth/internal/users/"+id.String()+"/permissions", &data); err != nil {
		return Permissions{}, err
	}
	if data.Admin == nil || data.UserID != id.String() {
		return Permissions{}, fmt.Errorf("%w: response is not for the requested user", ErrIdentityUnavailable)
	}
	p := Permissions{UserID: id.String(), Apps: data.Admin.Apps, Platform: data.Admin.Platform}
	if p.Apps == nil {
		p.Apps = map[string][]string{}
	}
	if p.Platform == nil {
		p.Platform = []string{}
	}
	return p, nil
}

// OtherTOTPHolders — GET /v1/auth/internal/permissions/:permission/holders?exclude_user_id=<uuid>
//
//	200 {"data":{"permission":"commerce:cod.settle","count":1}}
//
// holders counts active holders of the permission, other than exclude_user_id,
// who have TOTP enrolled. A missing or negative count is an error, never zero:
// zero is what lets one admin act alone.
func (c *IdentityClient) OtherTOTPHolders(ctx context.Context, permission, excludeUserID string) (int, error) {
	if !ValidPermission(permission) {
		return 0, fmt.Errorf("%w: invalid permission", ErrIdentityUnavailable)
	}
	id, err := uuid.Parse(excludeUserID)
	if err != nil {
		return 0, fmt.Errorf("%w: invalid user id", ErrIdentityUnavailable)
	}
	var data struct {
		Permission string `json:"permission"`
		Holders    *int   `json:"count"`
	}
	path := "/v1/auth/internal/permissions/" + url.PathEscape(permission) +
		"/holders?exclude_user_id=" + url.QueryEscape(id.String())
	if err := c.get(ctx, path, &data); err != nil {
		return 0, err
	}
	if data.Holders == nil || *data.Holders < 0 {
		return 0, fmt.Errorf("%w: holders count missing", ErrIdentityUnavailable)
	}
	if data.Permission != "" && data.Permission != permission {
		return 0, fmt.Errorf("%w: response is not for the requested permission", ErrIdentityUnavailable)
	}
	return *data.Holders, nil
}

// PermissionSource resolves one admin's permissions.
type PermissionSource interface {
	UserPermissions(ctx context.Context, userID string) (Permissions, error)
}

// PermissionCacheTTL bounds how stale an enforced permission may be: a revoked
// role stops working within this long.
const PermissionCacheTTL = 15 * time.Second

// CachedPermissions caches successful answers per user for a short TTL.
// Errors are never cached, so every failed lookup is retried and refused.
type CachedPermissions struct {
	src PermissionSource
	ttl time.Duration
	now func() time.Time

	mu      sync.Mutex
	entries map[string]cachedEntry
}

type cachedEntry struct {
	perms   Permissions
	expires time.Time
}

// NewCachedPermissions wraps src. ttl is clamped to (0, 30s].
func NewCachedPermissions(src PermissionSource, ttl time.Duration) *CachedPermissions {
	if ttl <= 0 || ttl > 30*time.Second {
		ttl = PermissionCacheTTL
	}
	return &CachedPermissions{src: src, ttl: ttl, now: time.Now, entries: map[string]cachedEntry{}}
}

func (c *CachedPermissions) UserPermissions(ctx context.Context, userID string) (Permissions, error) {
	now := c.now()
	c.mu.Lock()
	if e, ok := c.entries[userID]; ok && now.Before(e.expires) {
		c.mu.Unlock()
		return e.perms, nil
	}
	c.mu.Unlock()

	p, err := c.src.UserPermissions(ctx, userID)
	if err != nil {
		return Permissions{}, err
	}
	c.mu.Lock()
	if len(c.entries) > 10000 {
		c.entries = map[string]cachedEntry{}
	}
	c.entries[userID] = cachedEntry{perms: p, expires: now.Add(c.ttl)}
	c.mu.Unlock()
	return p, nil
}

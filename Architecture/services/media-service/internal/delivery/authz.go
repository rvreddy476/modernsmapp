package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Module 4 M4-P0-5 — content authorization for byte delivery.
//
// WHO DECIDES, AND WHY IT IS NOT THIS SERVICE
//
// media-service owns assets and processing. It does not own audiences. Whether
// a viewer may see a story is a fact about the story's visibility, the author's
// blocks, and the author's close-friends list — all of which live in
// post-service and graph-service. Reimplementing that here would create a
// second policy that is free to disagree with the first, and the disagreement
// would be invisible until someone saw media they should not have.
//
// So media-service asks the owning service and enforces the answer.
//
// FAIL CLOSED, ALWAYS
//
// Every uncertainty — transport error, non-200, malformed body, no owning
// content found, no authorizer configured — denies. There is deliberately no
// path where an unreachable dependency yields a served byte. The asymmetry is
// the point: a denied fetch during an outage is a broken image, while a
// permitted one during an outage is a privacy incident that nobody observes.

// ErrDeliveryDenied is a resolved denial: the viewer may not have these bytes.
var ErrDeliveryDenied = errors.New("delivery: not authorized")

// ErrDeliveryUnresolved means the decision could not be made. Callers map it to
// a retryable 503 and must not fall back to serving.
var ErrDeliveryUnresolved = errors.New("delivery: authorization unresolved")

var errBatchUnsupported = errors.New("delivery: batch authorization unsupported")

// ContentAuthorizer answers whether a viewer may receive an asset's bytes.
type ContentAuthorizer interface {
	// Authorize returns nil when the viewer may receive mediaID.
	Authorize(ctx context.Context, viewerID, mediaID string) error
}

type batchContentAuthorizer interface {
	AuthorizeBatch(ctx context.Context, viewerID string, mediaIDs []string) (map[string]bool, error)
}

// URLSigner is implemented by the CloudFront signer in production and by the
// local S3-compatible presigner in development. Keeping the gate provider-
// neutral lets local contract tests exercise the same authorization boundary
// without weakening the production CloudFront requirement.
type URLSigner interface {
	PublicURL(key string) (string, error)
	SignProtected(key string, ttl time.Duration, now time.Time) (string, error)
}

// HTTPContentAuthorizer asks post-service, which owns the content that
// references an asset.
type HTTPContentAuthorizer struct {
	baseURL        string
	path           string
	internalKey    string
	client         *http.Client
	allowAnonymous bool
}

func NewHTTPContentAuthorizer(baseURL, internalKey string, client *http.Client) *HTTPContentAuthorizer {
	if client == nil {
		// A short timeout on purpose. This call is on the media read path, and
		// a slow dependency must become a fast retryable failure rather than a
		// pile of held connections.
		client = &http.Client{Timeout: 3 * time.Second}
	}
	return &HTTPContentAuthorizer{
		baseURL:     strings.TrimRight(baseURL, "/"),
		path:        "/v1/internal/media-access",
		internalKey: internalKey,
		client:      client,
	}
}

func NewHTTPChatAuthorizer(baseURL, internalKey string, client *http.Client) *HTTPContentAuthorizer {
	authorizer := NewHTTPContentAuthorizer(baseURL, internalKey, client)
	authorizer.path = "/internal/v1/chat/media-access"
	return authorizer
}

func NewHTTPProfileAuthorizer(baseURL, internalKey string, client *http.Client) *HTTPContentAuthorizer {
	authorizer := NewHTTPContentAuthorizer(baseURL, internalKey, client)
	authorizer.path = "/v1/profiles/internal/media-access"
	// An anonymous viewer is a real audience category for public profile
	// photos. Profile-service resolves it against `who_can_see_profile_photo`;
	// post and chat authorities remain authenticated-only.
	authorizer.allowAnonymous = true
	return authorizer
}

func (a *HTTPContentAuthorizer) Authorize(ctx context.Context, viewerID, mediaID string) error {
	if a == nil || a.baseURL == "" {
		return fmt.Errorf("%w: no content authorizer configured", ErrDeliveryUnresolved)
	}
	if viewerID == "" && !a.allowAnonymous {
		// No viewer means no audience decision is possible. Protected bytes
		// have no anonymous reading.
		return fmt.Errorf("%w: no viewer", ErrDeliveryDenied)
	}

	body, err := json.Marshal(map[string]string{"viewer_id": viewerID, "media_id": mediaID})
	if err != nil {
		return fmt.Errorf("%w: encode request: %v", ErrDeliveryUnresolved, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.baseURL+a.path, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("%w: build request: %v", ErrDeliveryUnresolved, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if a.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", a.internalKey)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDeliveryUnresolved, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// A 200 still has to SAY yes. An empty or malformed body decoding to
		// the zero value would otherwise read as allowed=false anyway, but
		// being explicit keeps a future field rename from flipping the default.
		var out struct {
			Allowed bool `json:"allowed"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return fmt.Errorf("%w: decode response: %v", ErrDeliveryUnresolved, err)
		}
		if !out.Allowed {
			return ErrDeliveryDenied
		}
		return nil
	case http.StatusNotFound, http.StatusForbidden:
		// The owning service resolved the question and said no.
		return ErrDeliveryDenied
	default:
		return fmt.Errorf("%w: content authority returned %d", ErrDeliveryUnresolved, resp.StatusCode)
	}
}

// AuthorizeBatch asks the post content authority once for an entire feed page.
// Chat's authority does not expose this contract and explicitly falls back to
// individual checks only for assets post-service denied.
func (a *HTTPContentAuthorizer) AuthorizeBatch(ctx context.Context, viewerID string, mediaIDs []string) (map[string]bool, error) {
	if a == nil || a.baseURL == "" {
		return nil, fmt.Errorf("%w: no content authorizer configured", ErrDeliveryUnresolved)
	}
	if a.path != "/v1/internal/media-access" {
		return nil, errBatchUnsupported
	}
	body, err := json.Marshal(map[string]any{"viewer_id": viewerID, "media_ids": mediaIDs})
	if err != nil {
		return nil, fmt.Errorf("%w: encode batch request: %v", ErrDeliveryUnresolved, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.baseURL+a.path+"/batch", strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("%w: build batch request: %v", ErrDeliveryUnresolved, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if a.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", a.internalKey)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDeliveryUnresolved, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: content authority batch returned %d", ErrDeliveryUnresolved, resp.StatusCode)
	}
	var out struct {
		Allowed   map[string]bool   `json:"allowed"`
		Decisions map[string]string `json:"decisions,omitempty"`
		Reasons   map[string]string `json:"reasons,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("%w: decode batch response: %v", ErrDeliveryUnresolved, err)
	}
	if out.Allowed == nil {
		return nil, fmt.Errorf("%w: batch response omitted allowed map", ErrDeliveryUnresolved)
	}
	for id, allowed := range out.Allowed {
		decision := out.Decisions[id]
		reason := out.Reasons[id]
		if !allowed {
			slog.InfoContext(ctx, "content authority denied asset",
				"viewer_id", viewerID,
				"media_id", id,
				"decision", decision,
				"reason", reason)
		} else if decision == "not_ready" {
			slog.InfoContext(ctx, "content authority permitted asset (not ready)",
				"viewer_id", viewerID,
				"media_id", id,
				"reason", reason)
		}
	}
	return out.Allowed, nil
}

// AnyContentAuthorizer permits an asset when any canonical owning surface
// permits it. A denial from post-service may simply mean the asset belongs to
// chat; unresolved dependencies remain retryable unless another authority can
// positively resolve the request.
type AnyContentAuthorizer []ContentAuthorizer

func (authorizers AnyContentAuthorizer) Authorize(ctx context.Context, viewerID, mediaID string) error {
	if len(authorizers) == 0 {
		return fmt.Errorf("%w: no content authorities configured", ErrDeliveryUnresolved)
	}
	unresolved := false
	for _, authorizer := range authorizers {
		if authorizer == nil {
			unresolved = true
			continue
		}
		err := authorizer.Authorize(ctx, viewerID, mediaID)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrDeliveryDenied) {
			unresolved = true
		}
	}
	if unresolved {
		return ErrDeliveryUnresolved
	}
	return ErrDeliveryDenied
}

func (authorizers AnyContentAuthorizer) AuthorizeBatch(ctx context.Context, viewerID string, mediaIDs []string) (map[string]bool, error) {
	allowed := make(map[string]bool, len(mediaIDs))
	remaining := make(map[string]bool, len(mediaIDs))
	unresolved := make(map[string]bool, len(mediaIDs))
	for _, id := range mediaIDs {
		remaining[id] = true
	}
	for _, authorizer := range authorizers {
		if len(remaining) == 0 {
			break
		}
		if authorizer == nil {
			for id := range remaining {
				unresolved[id] = true
			}
			continue
		}
		ids := make([]string, 0, len(remaining))
		for id := range remaining {
			ids = append(ids, id)
		}
		if batcher, ok := authorizer.(batchContentAuthorizer); ok {
			batch, err := batcher.AuthorizeBatch(ctx, viewerID, ids)
			if err == nil {
				for id, yes := range batch {
					if yes && remaining[id] {
						allowed[id] = true
						delete(remaining, id)
						delete(unresolved, id)
					}
				}
				continue
			}
			if !errors.Is(err, errBatchUnsupported) {
				for _, id := range ids {
					unresolved[id] = true
				}
				continue
			}
		}
		for _, id := range ids {
			err := authorizer.Authorize(ctx, viewerID, id)
			switch {
			case err == nil:
				allowed[id] = true
				delete(remaining, id)
				delete(unresolved, id)
			case errors.Is(err, ErrDeliveryDenied):
				// Resolved denial by this authorizer; does not clear unresolved from other authorities.
			default:
				unresolved[id] = true
			}
		}
	}
	// Any asset that remains not allowed and encountered an unresolved error on
	// a candidate authorizer must fail the batch as unresolved.
	for id := range remaining {
		if unresolved[id] {
			return nil, ErrDeliveryUnresolved
		}
	}
	return allowed, nil
}

// Gate combines class detection, authorization and signing.
//
// It is the single entry point every media read path must use, so a new read
// endpoint cannot accidentally serve bytes by calling the store directly.
type Gate struct {
	signer URLSigner
	authz  ContentAuthorizer
}

func NewGate(signer URLSigner, authz ContentAuthorizer) *Gate {
	return &Gate{signer: signer, authz: authz}
}

// URLFor returns a delivery URL for objectKey, or an error.
//
// Public objects are returned as stable CDN URLs with no authorization call —
// that is what the public class means, and paying a round trip for an avatar on
// every render would make the common path the slow one.
//
// Protected objects are authorized first, then signed with a bounded TTL.
func (g *Gate) URLFor(ctx context.Context, viewerID, mediaID, objectKey string) (string, error) {
	if g == nil || g.signer == nil {
		return "", fmt.Errorf("%w: delivery gate not configured", ErrDeliveryUnresolved)
	}
	if ClassForKey(objectKey) == ClassPublic {
		return g.signer.PublicURL(objectKey)
	}
	if g.authz == nil {
		return "", fmt.Errorf("%w: no content authorizer for protected media", ErrDeliveryUnresolved)
	}
	if err := g.authz.Authorize(ctx, viewerID, mediaID); err != nil {
		return "", err
	}
	return g.signer.SignProtected(objectKey, MaxProtectedTTL, time.Now())
}

// URLsForAsset authorizes ONCE for the asset, then signs every key belonging to
// it (original, variants, HLS master).
//
// One authorization per asset rather than per key: the variants of an asset are
// the same content, so asking N times is N chances for a transient failure to
// produce a half-populated response, and N times the load on the content
// authority for one answer.
//
// A denial returns an error rather than an empty map. The caller must not
// render "no variants available" for a viewer who is actually blocked — that
// looks like a processing failure and invites a retry loop.
func (g *Gate) URLsForAsset(ctx context.Context, viewerID, mediaID string, keys map[string]string) (map[string]string, error) {
	if g == nil || g.signer == nil {
		return nil, fmt.Errorf("%w: delivery gate not configured", ErrDeliveryUnresolved)
	}
	if len(keys) == 0 {
		return map[string]string{}, nil
	}

	// An asset is protected if ANY of its keys is. Mixed classes would mean a
	// protected original with a public thumbnail, which leaks the content it is
	// a thumbnail of.
	protected := false
	for _, key := range keys {
		if ClassForKey(key) == ClassProtected {
			protected = true
			break
		}
	}
	if protected {
		if g.authz == nil {
			return nil, fmt.Errorf("%w: no content authorizer for protected media", ErrDeliveryUnresolved)
		}
		if err := g.authz.Authorize(ctx, viewerID, mediaID); err != nil {
			return nil, err
		}
	}

	now := time.Now()
	out := make(map[string]string, len(keys))
	for name, key := range keys {
		var (
			u   string
			err error
		)
		if ClassForKey(key) == ClassPublic {
			u, err = g.signer.PublicURL(key)
		} else {
			u, err = g.signer.SignProtected(key, MaxProtectedTTL, now)
		}
		if err != nil {
			// Reported, not skipped. A silently dropped variant is how a
			// signing misconfiguration turns into "video will not play" with
			// no error anywhere.
			return nil, fmt.Errorf("%w: sign %s: %v", ErrDeliveryUnresolved, name, err)
		}
		out[name] = u
	}
	return out, nil
}

// URLsForAssets is the true batch form used by feed hydration. It performs at
// most one post-authority HTTP call for all protected assets in the page,
// while preserving the existing omit-denied / fail-unresolved semantics.
func (g *Gate) URLsForAssets(ctx context.Context, viewerID string, assets map[string]map[string]string) (map[string]map[string]string, error) {
	if g == nil || g.signer == nil {
		return nil, fmt.Errorf("%w: delivery gate not configured", ErrDeliveryUnresolved)
	}
	result := make(map[string]map[string]string, len(assets))
	protected := make([]string, 0, len(assets))
	for mediaID, keys := range assets {
		needsAuth := false
		if len(keys) == 0 {
			needsAuth = true
		} else {
			for _, key := range keys {
				if ClassForKey(key) == ClassProtected {
					needsAuth = true
					break
				}
			}
		}
		if needsAuth {
			protected = append(protected, mediaID)
		}
	}

	allowed := make(map[string]bool, len(protected))
	if len(protected) > 0 {
		if g.authz == nil {
			return nil, fmt.Errorf("%w: no content authorizer configured", ErrDeliveryUnresolved)
		}
		if batcher, ok := g.authz.(batchContentAuthorizer); ok {
			batch, err := batcher.AuthorizeBatch(ctx, viewerID, protected)
			if err != nil {
				return nil, err
			}
			allowed = batch
		} else {
			for _, mediaID := range protected {
				err := g.authz.Authorize(ctx, viewerID, mediaID)
				if err == nil {
					allowed[mediaID] = true
					continue
				}
				if !errors.Is(err, ErrDeliveryDenied) {
					return nil, err
				}
			}
		}
	}

	now := time.Now()
	for mediaID, keys := range assets {
		urls := make(map[string]string, len(keys))
		denied := false
		if len(keys) == 0 {
			if !allowed[mediaID] {
				denied = true
			}
		}
		for name, key := range keys {
			var u string
			var err error
			if ClassForKey(key) == ClassPublic {
				u, err = g.signer.PublicURL(key)
			} else if allowed[mediaID] {
				u, err = g.signer.SignProtected(key, MaxProtectedTTL, now)
			} else {
				denied = true
				break
			}
			if err != nil {
				return nil, err
			}
			urls[name] = u
		}
		if !denied {
			result[mediaID] = urls
		}
	}
	return result, nil
}

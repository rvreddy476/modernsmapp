package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// ContentAuthorizer answers whether a viewer may receive an asset's bytes.
type ContentAuthorizer interface {
	// Authorize returns nil when the viewer may receive mediaID.
	Authorize(ctx context.Context, viewerID, mediaID string) error
}

// HTTPContentAuthorizer asks post-service, which owns the content that
// references an asset.
type HTTPContentAuthorizer struct {
	baseURL     string
	path        string
	internalKey string
	client      *http.Client
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

func (a *HTTPContentAuthorizer) Authorize(ctx context.Context, viewerID, mediaID string) error {
	if a == nil || a.baseURL == "" {
		return fmt.Errorf("%w: no content authorizer configured", ErrDeliveryUnresolved)
	}
	if viewerID == "" {
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

// Gate combines class detection, authorization and signing.
//
// It is the single entry point every media read path must use, so a new read
// endpoint cannot accidentally serve bytes by calling the store directly.
type Gate struct {
	signer *Signer
	authz  ContentAuthorizer
}

func NewGate(signer *Signer, authz ContentAuthorizer) *Gate {
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

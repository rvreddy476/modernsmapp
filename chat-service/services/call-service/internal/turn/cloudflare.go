// Package turn mints short-lived TURN credentials from a managed relay.
//
// Why this exists: the dev stack runs behind a Cloudflare Tunnel, which
// carries HTTP and WebSocket only. TURN and the media itself are UDP (with
// a TCP/TLS fallback) straight to a relay, and no tunnel can proxy that. So
// the in-stack coturn is reachable from this machine and its LAN and from
// nowhere else — every tester on another network had signalling and no
// media. A managed, anycast relay is the only fix that needs no port
// opened on anyone's router.
//
// Cloudflare's TURN service is used because the account already exists.
// Credentials are minted per join with a TTL and cached until they are
// close to expiry, so a burst of joins costs one API call, not one each.
package turn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/atpost/chat-call-service/internal/sfu"
)

// Documented at developers.cloudflare.com/realtime/turn/generate-credentials:
//
//	POST https://rtc.live.cloudflare.com/v1/turn/keys/{KEY_ID}/credentials/generate-ice-servers
//	Authorization: Bearer {API_TOKEN}
//	{"ttl": 86400}
//
// answers 201 with {"iceServers": [ {urls…}, {urls…, username, credential} ]}.
const cloudflareEndpoint = "https://rtc.live.cloudflare.com/v1/turn/keys/%s/credentials/generate-ice-servers"

// Cloudflare mints TURN credentials from Cloudflare's TURN service.
type Cloudflare struct {
	keyID    string
	apiToken string
	ttl      time.Duration
	client   *http.Client
	endpoint string // overridable for tests

	mu        sync.Mutex
	cached    []sfu.ICEServer
	expiresAt time.Time
}

// NewCloudflare returns a minter. ttl is how long each credential lives; it
// must comfortably exceed the longest call, because a credential expiring
// mid-call drops the relay under it.
func NewCloudflare(keyID, apiToken string, ttl time.Duration) (*Cloudflare, error) {
	keyID = strings.TrimSpace(keyID)
	apiToken = strings.TrimSpace(apiToken)
	if keyID == "" || apiToken == "" {
		return nil, errors.New("cloudflare turn: key id and api token are both required")
	}
	if ttl < time.Minute {
		return nil, fmt.Errorf("cloudflare turn: ttl %s is too short to cover a call", ttl)
	}
	return &Cloudflare{
		keyID:    keyID,
		apiToken: apiToken,
		ttl:      ttl,
		client:   &http.Client{Timeout: 5 * time.Second},
		endpoint: fmt.Sprintf(cloudflareEndpoint, keyID),
	}, nil
}

// ICEServers returns a credentialled ICE server list, minting a new one
// when the cached set is within a quarter of its TTL of expiring. A cached
// set is shared across joins on purpose: each credential is a bearer of
// relay bandwidth, not an identity, and per-user credentials would mean
// one Cloudflare round trip on every join.
func (c *Cloudflare) ICEServers(ctx context.Context) ([]sfu.ICEServer, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.cached) > 0 && time.Until(c.expiresAt) > c.ttl/4 {
		return clone(c.cached), nil
	}

	servers, err := c.mint(ctx)
	if err != nil {
		// A still-valid cached set beats an error; only a genuinely expired
		// cache surfaces the failure to the caller.
		if len(c.cached) > 0 && time.Now().Before(c.expiresAt) {
			return clone(c.cached), nil
		}
		return nil, err
	}
	c.cached = servers
	c.expiresAt = time.Now().Add(c.ttl)
	return clone(servers), nil
}

func (c *Cloudflare) mint(ctx context.Context) ([]sfu.ICEServer, error) {
	body, _ := json.Marshal(map[string]int64{"ttl": int64(c.ttl / time.Second)})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	req.Header.Set("Content-Type", "application/json")

	res, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cloudflare turn: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		// The body is Cloudflare's, never the token; safe to log as-is.
		return nil, fmt.Errorf("cloudflare turn: status %d: %s", res.StatusCode, strings.TrimSpace(string(raw)))
	}

	servers, err := parseICEServers(raw)
	if err != nil {
		return nil, err
	}
	return servers, nil
}

// parseICEServers accepts the documented array form and, defensively, a
// single-object form — the docs' examples have shown both over time.
func parseICEServers(raw []byte) ([]sfu.ICEServer, error) {
	var envelope struct {
		ICEServers json.RawMessage `json:"iceServers"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("cloudflare turn: parse response: %w", err)
	}
	if len(envelope.ICEServers) == 0 {
		return nil, errors.New("cloudflare turn: response has no iceServers")
	}

	type entry struct {
		URLs       any    `json:"urls"`
		Username   string `json:"username"`
		Credential string `json:"credential"`
	}
	var entries []entry
	if bytes.HasPrefix(bytes.TrimSpace(envelope.ICEServers), []byte("[")) {
		if err := json.Unmarshal(envelope.ICEServers, &entries); err != nil {
			return nil, fmt.Errorf("cloudflare turn: parse iceServers: %w", err)
		}
	} else {
		var one entry
		if err := json.Unmarshal(envelope.ICEServers, &one); err != nil {
			return nil, fmt.Errorf("cloudflare turn: parse iceServers: %w", err)
		}
		entries = []entry{one}
	}

	out := make([]sfu.ICEServer, 0, len(entries))
	for _, e := range entries {
		var urls []string
		switch v := e.URLs.(type) {
		case string:
			if s := strings.TrimSpace(v); s != "" {
				urls = []string{s}
			}
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
					urls = append(urls, strings.TrimSpace(s))
				}
			}
		}
		if len(urls) == 0 {
			continue
		}
		out = append(out, sfu.ICEServer{URLs: urls, Username: e.Username, Credential: e.Credential})
	}
	if len(out) == 0 {
		return nil, errors.New("cloudflare turn: no usable ICE servers in response")
	}
	hasRelay := false
	for _, s := range out {
		for _, u := range s.URLs {
			if strings.HasPrefix(u, "turn:") || strings.HasPrefix(u, "turns:") {
				hasRelay = true
			}
		}
	}
	if !hasRelay {
		return nil, errors.New("cloudflare turn: response carried no TURN relay")
	}
	return out, nil
}

func clone(in []sfu.ICEServer) []sfu.ICEServer {
	out := make([]sfu.ICEServer, 0, len(in))
	for _, s := range in {
		out = append(out, sfu.ICEServer{
			URLs:       append([]string(nil), s.URLs...),
			Username:   s.Username,
			Credential: s.Credential,
		})
	}
	return out
}

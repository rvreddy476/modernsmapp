// Package livekit is a thin client wrapping the LiveKit server SDK
// surface that live-service-v2 actually needs:
//
//   - access-token issuance (publisher / subscriber)
//   - room admin create
//   - egress start / stop to an S3-compatible target
//
// We deliberately do NOT import github.com/livekit/server-sdk-go because
// (a) the SDK pulls a hundred MB of protobuf code into the monorepo's
// vendor tree for what is, in practice, two HTTP calls and a JWT, and
// (b) keeping the surface area behind a small interface lets the service
// layer unit-test without any LiveKit dependency at all.
//
// Tokens are signed JWTs (HMAC-SHA256) per the LiveKit token spec:
//
//	https://docs.livekit.io/realtime/concepts/authentication/
//
// Room admin and Egress operations target the LiveKit Twirp endpoints
// over plain HTTP+JSON; the auth header is an admin JWT (same signing
// scheme as the participant token, just with `video.roomAdmin=true`).
package livekit

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"
)

// Client is the surface live-service-v2's service layer talks to. Tests
// substitute a fake implementing this interface.
type Client interface {
	CreateRoom(ctx context.Context, room string) error
	IssuePublisherToken(ctx context.Context, room, identity string, ttl time.Duration) (string, error)
	IssueViewerToken(ctx context.Context, room, identity string, ttl time.Duration) (string, error)
	StartEgressToS3(ctx context.Context, room, objectKey string) (egressID string, err error)
	StopEgress(ctx context.Context, egressID string) error
	ServerURL() string
}

// Config carries the LiveKit + S3 credentials live-service-v2 needs.
type Config struct {
	APIKey    string
	APISecret string
	URL       string // wss:// URL for the SFU returned to clients

	// Egress S3 target — reused from the platform's MinIO config.
	S3Endpoint  string
	S3AccessKey string
	S3SecretKey string
	S3Bucket    string
	S3Region    string
	S3UseSSL    bool
}

type httpClient struct {
	cfg  Config
	http *http.Client
}

// New returns a LiveKit Client backed by HTTPS calls. If cfg.APIKey is
// empty the returned client returns errors on every operation — useful
// for keeping main.go alive in dev when LiveKit isn't configured yet.
func New(cfg Config) Client {
	return &httpClient{
		cfg:  cfg,
		http: &http.Client{Timeout: 8 * time.Second},
	}
}

func (c *httpClient) ServerURL() string { return c.cfg.URL }

// CreateRoom POSTs to /twirp/livekit.RoomService/CreateRoom. It is
// idempotent on LiveKit's side; if the room already exists we silently
// accept the conflict.
func (c *httpClient) CreateRoom(ctx context.Context, room string) error {
	if err := c.requireConfigured(); err != nil {
		return err
	}
	body := map[string]any{
		"name":            room,
		"empty_timeout":   300, // garbage-collect 5 min after last participant
		"max_participants": 10000,
	}
	return c.twirpCall(ctx, "/twirp/livekit.RoomService/CreateRoom", body, nil)
}

func (c *httpClient) IssuePublisherToken(ctx context.Context, room, identity string, ttl time.Duration) (string, error) {
	if err := c.requireConfigured(); err != nil {
		return "", err
	}
	return c.signAccessToken(identity, ttl, map[string]any{
		"room":           room,
		"roomJoin":       true,
		"canPublish":     true,
		"canPublishData": true,
		"canSubscribe":   true,
	})
}

func (c *httpClient) IssueViewerToken(ctx context.Context, room, identity string, ttl time.Duration) (string, error) {
	if err := c.requireConfigured(); err != nil {
		return "", err
	}
	return c.signAccessToken(identity, ttl, map[string]any{
		"room":           room,
		"roomJoin":       true,
		"canPublish":     false,
		"canPublishData": false,
		"canSubscribe":   true,
	})
}

// StartEgressToS3 starts a composite RoomEgress writing one MP4 file to
// the configured S3 bucket at `objectKey`. Returns the LiveKit-issued
// egress_id (recorded on the live_streams row so EndStream can stop it
// idempotently). The webhook fired on completion carries the same ID.
func (c *httpClient) StartEgressToS3(ctx context.Context, room, objectKey string) (string, error) {
	if err := c.requireConfigured(); err != nil {
		return "", err
	}
	body := map[string]any{
		"room_name": room,
		"file": map[string]any{
			"file_type": "MP4",
			"filepath":  objectKey,
			"s3": map[string]any{
				"access_key": c.cfg.S3AccessKey,
				"secret":     c.cfg.S3SecretKey,
				"region":     c.cfg.S3Region,
				"bucket":     c.cfg.S3Bucket,
				"endpoint":   c.cfg.S3Endpoint,
				"force_path_style": true,
			},
		},
	}
	var resp struct {
		EgressID string `json:"egress_id"`
	}
	if err := c.twirpCall(ctx, "/twirp/livekit.Egress/StartRoomCompositeEgress", body, &resp); err != nil {
		return "", err
	}
	return resp.EgressID, nil
}

func (c *httpClient) StopEgress(ctx context.Context, egressID string) error {
	if err := c.requireConfigured(); err != nil {
		return err
	}
	if egressID == "" {
		return nil
	}
	return c.twirpCall(ctx, "/twirp/livekit.Egress/StopEgress", map[string]any{
		"egress_id": egressID,
	}, nil)
}

func (c *httpClient) requireConfigured() error {
	if c.cfg.APIKey == "" || c.cfg.APISecret == "" {
		return fmt.Errorf("livekit: API key/secret not configured")
	}
	if c.cfg.URL == "" {
		return fmt.Errorf("livekit: URL not configured")
	}
	return nil
}

// twirpCall POSTs JSON to the LiveKit Twirp endpoint at cfg.URL. The
// caller's ws/wss URL is reused (LiveKit serves Twirp on the same host,
// http/https scheme). resp may be nil if the caller does not care about
// the response body.
func (c *httpClient) twirpCall(ctx context.Context, path string, body any, resp any) error {
	adminToken, err := c.signAccessToken("live-service-v2", 10*time.Minute, map[string]any{
		"roomAdmin":  true,
		"roomCreate": true,
		"roomRecord": true,
	})
	if err != nil {
		return err
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	url := httpURL(c.cfg.URL) + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)

	r, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("livekit: %s: %w", path, err)
	}
	defer r.Body.Close()
	if r.StatusCode >= 400 {
		body, _ := io.ReadAll(r.Body)
		return fmt.Errorf("livekit: %s: status %d: %s", path, r.StatusCode, strings.TrimSpace(string(body)))
	}
	if resp != nil {
		return json.NewDecoder(r.Body).Decode(resp)
	}
	return nil
}

// signAccessToken builds and HMAC-SHA256-signs a LiveKit access token.
// The `video` grant claim contains the per-token capability set.
func (c *httpClient) signAccessToken(identity string, ttl time.Duration, videoGrant map[string]any) (string, error) {
	now := time.Now()
	if ttl <= 0 {
		ttl = time.Hour
	}
	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	claims := map[string]any{
		"iss":   c.cfg.APIKey,
		"sub":   identity,
		"nbf":   now.Unix() - 30,
		"exp":   now.Add(ttl).Unix(),
		"iat":   now.Unix(),
		"jti":   newJTI(),
		"name":  identity,
		"video": videoGrant,
	}
	hb, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	signingInput := enc.EncodeToString(hb) + "." + enc.EncodeToString(cb)
	mac := hmac.New(sha256.New, []byte(c.cfg.APISecret))
	mac.Write([]byte(signingInput))
	sig := enc.EncodeToString(mac.Sum(nil))
	return signingInput + "." + sig, nil
}

// httpURL converts a ws(s):// LiveKit URL to its http(s):// peer for
// Twirp calls. Anything else is returned unchanged.
func httpURL(u string) string {
	switch {
	case strings.HasPrefix(u, "wss://"):
		return "https://" + strings.TrimPrefix(u, "wss://")
	case strings.HasPrefix(u, "ws://"):
		return "http://" + strings.TrimPrefix(u, "ws://")
	}
	return u
}

func newJTI() string {
	b := make([]byte, 12)
	// math/rand is fine here; jti only needs to be unique per token, not
	// cryptographically random — the JWT signature is what authenticates.
	rand.Read(b)
	return hex.EncodeToString(b)
}

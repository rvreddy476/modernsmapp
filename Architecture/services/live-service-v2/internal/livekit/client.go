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
	"errors"
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

	// DeleteRoom closes the room for everyone (admin stop, host lost).
	// A room that no longer exists is not an error.
	DeleteRoom(ctx context.Context, room string) error
	// RemoveParticipant disconnects one identity (a ban takes effect on a
	// viewer already in the room). Not-found is not an error.
	RemoveParticipant(ctx context.Context, room, identity string) error
	// ListParticipants returns the room's participants. ErrRoomNotFound
	// when LiveKit says the room does not exist.
	ListParticipants(ctx context.Context, room string) ([]Participant, error)
}

// Participant is the slice of LiveKit's ParticipantInfo the sweeper reads.
type Participant struct {
	Identity string             `json:"identity"`
	Tracks   []ParticipantTrack `json:"tracks"`
}

// ParticipantTrack is one published track.
type ParticipantTrack struct {
	Sid string `json:"sid"`
}

// ErrRoomNotFound is LiveKit's twirp "not_found" for a room.
var ErrRoomNotFound = errors.New("livekit: room not found")

// Config carries the LiveKit + S3 credentials live-service-v2 needs.
type Config struct {
	APIKey    string
	APISecret string
	URL       string // ws(s):// URL this SERVICE uses for Twirp admin calls

	// PublicURL is the ws(s):// URL handed to CLIENTS (publisher and
	// viewer token results). It differs from URL whenever the service and
	// the devices live on different networks — in the dev stack the
	// container reaches LiveKit as ws://livekit:7880 while the emulator
	// needs ws://10.0.2.2:7880. Empty falls back to URL.
	PublicURL string

	// Egress S3 target — reused from the platform's MinIO config.
	S3Endpoint string
	// EgressS3Endpoint (LIVE_EGRESS_S3_ENDPOINT) is the S3 endpoint put in
	// StartEgress requests only; empty = S3Endpoint.
	EgressS3Endpoint string
	S3AccessKey      string
	S3SecretKey      string
	S3Bucket         string
	S3Region         string
	S3UseSSL         bool
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

func (c *httpClient) ServerURL() string {
	if c.cfg.PublicURL != "" {
		return c.cfg.PublicURL
	}
	return c.cfg.URL
}

// CreateRoom POSTs to /twirp/livekit.RoomService/CreateRoom. It is
// idempotent on LiveKit's side; if the room already exists we silently
// accept the conflict.
func (c *httpClient) CreateRoom(ctx context.Context, room string) error {
	if err := c.requireConfigured(); err != nil {
		return err
	}
	body := map[string]any{
		"name":             room,
		"empty_timeout":    300, // garbage-collect 5 min after last participant
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
	s3 := map[string]any{
		"region":           c.cfg.S3Region,
		"bucket":           c.cfg.S3Bucket,
		"endpoint":         c.egressS3Endpoint(),
		"force_path_style": true,
	}
	// Static credentials only when both are configured. Without them the
	// egress worker uses its own ambient credentials (an IAM role in prod);
	// an empty pair would instead be sent as "use these empty keys".
	if c.cfg.S3AccessKey != "" && c.cfg.S3SecretKey != "" {
		s3["access_key"] = c.cfg.S3AccessKey
		s3["secret"] = c.cfg.S3SecretKey
	}
	body := map[string]any{
		"room_name": room,
		"file": map[string]any{
			"file_type": "MP4",
			"filepath":  objectKey,
			"s3":        s3,
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

// egressS3Endpoint is where the EGRESS worker writes. It differs from the
// service's own S3Endpoint when egress runs elsewhere (LiveKit Cloud cannot
// reach http://minio:9000); empty falls back to S3Endpoint.
func (c *httpClient) egressS3Endpoint() string {
	if c.cfg.EgressS3Endpoint != "" {
		return c.cfg.EgressS3Endpoint
	}
	return c.cfg.S3Endpoint
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

// DeleteRoom POSTs /twirp/livekit.RoomService/DeleteRoom. Every participant
// is disconnected and LiveKit fires room_finished.
func (c *httpClient) DeleteRoom(ctx context.Context, room string) error {
	if err := c.requireConfigured(); err != nil {
		return err
	}
	err := c.twirpCall(ctx, "/twirp/livekit.RoomService/DeleteRoom", map[string]any{"room": room}, nil)
	if errors.Is(err, ErrRoomNotFound) {
		return nil
	}
	return err
}

// RemoveParticipant POSTs /twirp/livekit.RoomService/RemoveParticipant.
func (c *httpClient) RemoveParticipant(ctx context.Context, room, identity string) error {
	if err := c.requireConfigured(); err != nil {
		return err
	}
	err := c.twirpCallRoom(ctx, "/twirp/livekit.RoomService/RemoveParticipant", room,
		map[string]any{"room": room, "identity": identity}, nil)
	if errors.Is(err, ErrRoomNotFound) {
		return nil
	}
	return err
}

// ListParticipants POSTs /twirp/livekit.RoomService/ListParticipants.
func (c *httpClient) ListParticipants(ctx context.Context, room string) ([]Participant, error) {
	if err := c.requireConfigured(); err != nil {
		return nil, err
	}
	var resp struct {
		Participants []Participant `json:"participants"`
	}
	if err := c.twirpCallRoom(ctx, "/twirp/livekit.RoomService/ListParticipants", room, map[string]any{"room": room}, &resp); err != nil {
		return nil, err
	}
	return resp.Participants, nil
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
	return c.twirpCallRoom(ctx, path, "", body, resp)
}

// twirpCallRoom is twirpCall with the admin grant scoped to one room:
// LiveKit's RoomService checks roomAdmin AND grant.room == request room for
// ListParticipants / RemoveParticipant (server-sdk-go roomclient.go signs
// withVideoGrant{RoomAdmin: true, Room: req.Room} for exactly these).
func (c *httpClient) twirpCallRoom(ctx context.Context, path, room string, body any, resp any) error {
	grant := map[string]any{
		"roomAdmin":  true,
		"roomCreate": true,
		"roomRecord": true,
	}
	if room != "" {
		grant["room"] = room
	}
	adminToken, err := c.signAccessToken("live-service-v2", 10*time.Minute, grant)
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
		body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		// Twirp errors are {"code":"not_found","msg":...} with HTTP 404.
		if r.StatusCode == http.StatusNotFound {
			var te struct {
				Code string `json:"code"`
			}
			if json.Unmarshal(body, &te) == nil && te.Code == "not_found" {
				return fmt.Errorf("%w: %s", ErrRoomNotFound, path)
			}
		}
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

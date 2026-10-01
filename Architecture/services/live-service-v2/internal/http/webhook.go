package http

// LiveKit webhooks — POST /v1/livestream/webhooks/livekit (1 Oct 2026).
//
// Verified against LiveKit's own receiver, github.com/livekit/protocol
// webhook/verifier.go Receive (the function server-sdk-go's webhook package
// re-exports), and the signer, webhook/url_notifier.go send:
//
//   - the body is protojson.Marshal(WebhookEvent), sent with
//     Content-Type application/webhook+json;
//   - the Authorization header is a bare JWT (no "Bearer "), HS256 signed
//     with the API secret: auth.NewAccessToken(apiKey, apiSecret).
//     SetValidFor(5m).SetSha256(b64).ToJWT() — iss = the API key, nbf/exp
//     set, and the claim "sha256" = base64.StdEncoding(sha256(body));
//   - Receive parses the token, looks the secret up by iss, verifies the
//     signature and exp/nbf (go-jose, 1 minute leeway), then compares the
//     sha256 claim with the body hash in constant time.
//
// This handler does the same with one configured key: alg must be HS256,
// iss must equal LIVEKIT_API_KEY, the HMAC must verify under
// LIVEKIT_API_SECRET, exp must be present and not passed, nbf not in the
// future (60s leeway each), and the sha256 claim must match the raw body.
// With no key or secret configured EVERY request is refused — there is no
// accept-everything mode.
//
// protojson uses lowerCamelCase field names and encodes int64 as strings
// ("egressInfo", "createdAt": "1700000000"); the parser accepts those and
// snake_case alike.

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"

	"github.com/atpost/live-service-v2/internal/service"
	"github.com/atpost/shared/api"
)

// WebhookPath is where LiveKit posts (livekit.yaml webhook.urls).
const WebhookPath = "/v1/livestream/webhooks/livekit"

// webhookLeeway matches go-jose's default for exp/nbf.
const webhookLeeway = time.Minute

const maxWebhookBody = 1 << 20

// Webhook verification failures.
var (
	ErrWebhookNotConfigured = errors.New("webhook verification is not configured")
	ErrWebhookNoAuth        = errors.New("authorization header missing")
	ErrWebhookMalformed     = errors.New("authorization token malformed")
	ErrWebhookAlg           = errors.New("authorization token algorithm is not HS256")
	ErrWebhookSignature     = errors.New("authorization token signature invalid")
	ErrWebhookIssuer        = errors.New("authorization token issuer is not the configured API key")
	ErrWebhookExpired       = errors.New("authorization token expired or not yet valid")
	ErrWebhookChecksum      = errors.New("body does not match the signed sha256")
)

// VerifyLiveKitWebhook checks LiveKit's webhook signature over body.
func VerifyLiveKitWebhook(authHeader string, body []byte, apiKey, apiSecret string, now time.Time) error {
	if apiKey == "" || apiSecret == "" {
		return ErrWebhookNotConfigured
	}
	token := strings.TrimSpace(authHeader)
	if len(token) > 7 && strings.EqualFold(token[:7], "Bearer ") {
		token = strings.TrimSpace(token[7:])
	}
	if token == "" {
		return ErrWebhookNoAuth
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ErrWebhookMalformed
	}
	hb, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return ErrWebhookMalformed
	}
	var hdr struct {
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(hb, &hdr); err != nil {
		return ErrWebhookMalformed
	}
	if hdr.Alg != "HS256" {
		return ErrWebhookAlg
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return ErrWebhookMalformed
	}
	mac := hmac.New(sha256.New, []byte(apiSecret))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return ErrWebhookSignature
	}
	cb, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ErrWebhookMalformed
	}
	var claims struct {
		Iss    string   `json:"iss"`
		Exp    *float64 `json:"exp"`
		Nbf    *float64 `json:"nbf"`
		Sha256 string   `json:"sha256"`
	}
	if err := json.Unmarshal(cb, &claims); err != nil {
		return ErrWebhookMalformed
	}
	if subtle.ConstantTimeCompare([]byte(claims.Iss), []byte(apiKey)) != 1 {
		return ErrWebhookIssuer
	}
	if claims.Exp == nil || now.After(time.Unix(int64(*claims.Exp), 0).Add(webhookLeeway)) {
		return ErrWebhookExpired
	}
	if claims.Nbf != nil && now.Add(webhookLeeway).Before(time.Unix(int64(*claims.Nbf), 0)) {
		return ErrWebhookExpired
	}
	sum := sha256.Sum256(body)
	want := base64.StdEncoding.EncodeToString(sum[:])
	if claims.Sha256 == "" || subtle.ConstantTimeCompare([]byte(claims.Sha256), []byte(want)) != 1 {
		return ErrWebhookChecksum
	}
	return nil
}

// OnLiveKitWebhook verifies, parses and applies one LiveKit event.
// 401 on any verification failure; 400 on a verified but unparseable body;
// 500 when applying failed (LiveKit retries); 200 otherwise.
func (h *Handler) OnLiveKitWebhook(c *gin.Context) {
	ctx := c.Request.Context()
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxWebhookBody+1))
	if err != nil || len(body) > maxWebhookBody {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "unreadable body", nil)
		return
	}
	now := time.Now
	if h.clock != nil {
		now = h.clock
	}
	if err := VerifyLiveKitWebhook(c.GetHeader("Authorization"), body, h.webhookKey, h.webhookSecret, now()); err != nil {
		slog.WarnContext(ctx, "live-v2 webhook refused", "reason", err.Error())
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "invalid webhook signature", nil)
		return
	}
	ev, err := ParseLiveKitWebhook(body)
	if err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "unparseable webhook body", nil)
		return
	}
	if err := h.svc.HandleWebhook(ctx, ev); err != nil {
		slog.ErrorContext(ctx, "live-v2 webhook apply failed", "event", ev.Event, "id", ev.ID, "err", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "webhook not applied", nil)
		return
	}
	c.Status(http.StatusOK)
}

// flexInt64 accepts a JSON number or a numeric string (protojson int64).
type flexInt64 int64

func (f *flexInt64) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		fl, ferr := strconv.ParseFloat(s, 64)
		if ferr != nil {
			return err
		}
		n = int64(fl)
	}
	*f = flexInt64(n)
	return nil
}

type lkTrack struct {
	Sid string `json:"sid"`
}

type lkWebhook struct {
	ID    string `json:"id"`
	Event string `json:"event"`
	Room  *struct {
		Name string `json:"name"`
	} `json:"room"`
	Participant *struct {
		Identity string    `json:"identity"`
		Tracks   []lkTrack `json:"tracks"`
	} `json:"participant"`
	Track      *lkTrack `json:"track"`
	EgressInfo *struct {
		EgressID    string   `json:"egressId"`
		RoomName    string   `json:"roomName"`
		Status      string   `json:"status"`
		File        *lkFile  `json:"file"`
		FileResults []lkFile `json:"fileResults"`
	} `json:"egressInfo"`
}

type lkFile struct {
	Filename string    `json:"filename"`
	Location string    `json:"location"`
	Duration flexInt64 `json:"duration"` // nanoseconds
}

// ParseLiveKitWebhook decodes a (verified) WebhookEvent body.
func ParseLiveKitWebhook(body []byte) (service.WebhookEvent, error) {
	var raw any
	if err := json.Unmarshal(body, &raw); err != nil {
		return service.WebhookEvent{}, err
	}
	norm, err := json.Marshal(camelKeys(raw))
	if err != nil {
		return service.WebhookEvent{}, err
	}
	var w lkWebhook
	if err := json.Unmarshal(norm, &w); err != nil {
		return service.WebhookEvent{}, err
	}
	ev := service.WebhookEvent{ID: w.ID, Event: w.Event}
	if w.Room != nil {
		ev.Room = w.Room.Name
	}
	if w.Participant != nil {
		ev.ParticipantIdentity = w.Participant.Identity
		for _, t := range w.Participant.Tracks {
			ev.ParticipantTrackSIDs = append(ev.ParticipantTrackSIDs, t.Sid)
		}
	}
	if w.Track != nil {
		ev.TrackSID = w.Track.Sid
	}
	if e := w.EgressInfo; e != nil {
		res := &service.EgressResult{EgressID: e.EgressID, RoomName: e.RoomName, Status: e.Status}
		f := e.File
		if len(e.FileResults) > 0 {
			f = &e.FileResults[0]
		}
		if f != nil {
			res.Location = f.Location
			res.Filename = f.Filename
			res.DurationNs = int64(f.Duration)
		}
		ev.Egress = res
	}
	return ev, nil
}

// camelKeys rewrites every object key from snake_case to lowerCamelCase
// (protojson's JSON names), leaving camelCase keys unchanged.
func camelKeys(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[snakeToCamel(k)] = camelKeys(val)
		}
		return out
	case []any:
		for i := range t {
			t[i] = camelKeys(t[i])
		}
		return t
	default:
		return v
	}
}

func snakeToCamel(s string) string {
	if !strings.Contains(s, "_") {
		return s
	}
	var b strings.Builder
	up := false
	for _, r := range s {
		if r == '_' {
			up = true
			continue
		}
		if up {
			b.WriteRune(unicode.ToUpper(r))
			up = false
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

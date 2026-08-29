package push

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// FCMPusher sends push notifications via Firebase Cloud Messaging HTTP v1 API.
//
// Audit CR3: previously this used `getAccessToken()` which returned the
// empty string, so every request sent `Authorization: Bearer ` and got
// 401 UNAUTHENTICATED from FCM. Android and Web push were silently
// broken for the entire lifetime of this code. Now it implements the
// service-account JWT-bearer flow against the Google token endpoint
// using only the stdlib (no new module dependencies).
type FCMPusher struct {
	projectID          string
	serviceAccountJSON string
	httpClient         *http.Client

	// apiBaseURL overrides the FCM endpoint for adapter fixture tests;
	// empty means the real https://fcm.googleapis.com.
	apiBaseURL string

	// Parsed service account, populated lazily on first send.
	saOnce sync.Once
	sa     *fcmServiceAccount
	saErr  error

	// Cached access token. Reused until ~5 minutes before expiry to
	// avoid hammering Google's token endpoint on every push.
	tokenMu     sync.Mutex
	accessToken string
	tokenExpiry time.Time
}

type fcmServiceAccount struct {
	ClientEmail  string `json:"client_email"`
	PrivateKey   string `json:"private_key"`
	PrivateKeyID string `json:"private_key_id"`
	TokenURI     string `json:"token_uri"`

	parsedKey *rsa.PrivateKey
}

// NewFCMPusher creates an FCM pusher. serviceAccountJSON is the Firebase service account JSON.
func NewFCMPusher(projectID, serviceAccountJSON string) *FCMPusher {
	return &FCMPusher{
		projectID:          projectID,
		serviceAccountJSON: serviceAccountJSON,
		httpClient:         &http.Client{Timeout: 10 * time.Second},
	}
}

// callPushTypes are the ringing/call types that use Android's calling push
// contract (CALL-LB-4): DATA-ONLY — a `notification` block would be
// system-rendered in background as an ordinary tray card and NEVER reach
// FirebaseMessagingService, so the app could not show its full-screen
// incoming-call UI — and HIGH priority, so a Dozing device still wakes.
// Title/body ride in `data` for the app's own presenter.
var callPushTypes = map[string]bool{
	"incoming_call":       true,
	"incoming_video_call": true,
}

// BuildFCMMessage shapes one FCM v1 `message` object. Exported (and pure) so
// the calling contract is pinned by tests without an FCM round trip.
func BuildFCMMessage(token, title, body string, data map[string]string) map[string]interface{} {
	android := map[string]interface{}{}
	if ck, ok := data["collapse_key"]; ok && ck != "" {
		// FCM collapse_key: the latest notification replaces older ones
		// with the same key.
		android["collapse_key"] = ck
	}

	if callPushTypes[data["type"]] {
		// Data-only + high priority: the app renders the ring itself.
		enriched := make(map[string]string, len(data)+2)
		for k, v := range data {
			enriched[k] = v
		}
		enriched["title"] = title
		enriched["body"] = body
		android["priority"] = "high"
		return map[string]interface{}{
			"token":   token,
			"data":    enriched,
			"android": android,
		}
	}

	msg := map[string]interface{}{
		"token": token,
		"notification": map[string]string{
			"title": title,
			"body":  body,
		},
		"data": data,
	}
	if len(android) > 0 {
		msg["android"] = android
	}
	return msg
}

// Send delivers a push notification via FCM to an Android or Web device.
func (f *FCMPusher) Send(ctx context.Context, token, platform, title, body string, data map[string]string) error {
	if f.projectID == "" || f.serviceAccountJSON == "" {
		// CALL-LB-4: an unconfigured provider is no longer silent success —
		// the caller decides whether a skipped push is acceptable.
		return fmt.Errorf("fcm credentials missing: %w", ErrProviderNotConfigured)
	}

	accessToken, err := f.getAccessToken(ctx)
	if err != nil {
		return fmt.Errorf("fcm: get access token: %w", err)
	}

	msg := BuildFCMMessage(token, title, body, data)

	payload := map[string]interface{}{
		"message": msg,
	}

	b, _ := json.Marshal(payload)
	base := f.apiBaseURL
	if base == "" {
		base = "https://fcm.googleapis.com"
	}
	apiURL := fmt.Sprintf("%s/v1/projects/%s/messages:send", base, f.projectID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		// CALL-LB-4A: the RESPONSE BODY decides, not the status — FCM uses
		// 400 for invalid tokens AND invalid requests, distinguished only
		// by error.details. A dead token retires; a request/config/auth/
		// quota failure propagates without blaming a healthy device.
		if isFCMTokenRejection(body) {
			return fmt.Errorf("fcm: status %d: %s: %w",
				resp.StatusCode, string(body), ErrDeviceRejected)
		}
		return fmt.Errorf("fcm: send failed with status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// getAccessToken returns a cached OAuth2 access token for the FCM
// service account, refreshing it from the Google token endpoint when
// it's missing or within 5 minutes of expiry. Audit CR3: this used to
// return "" unconditionally.
func (f *FCMPusher) getAccessToken(ctx context.Context) (string, error) {
	if err := f.loadServiceAccount(); err != nil {
		return "", err
	}

	f.tokenMu.Lock()
	defer f.tokenMu.Unlock()
	if f.accessToken != "" && time.Until(f.tokenExpiry) > 5*time.Minute {
		return f.accessToken, nil
	}

	assertion, err := f.buildSignedJWT()
	if err != nil {
		return "", fmt.Errorf("sign JWT: %w", err)
	}

	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	form.Set("assertion", assertion)

	tokenURI := f.sa.TokenURI
	if tokenURI == "" {
		tokenURI = "https://oauth2.googleapis.com/token"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", fmt.Errorf("parse token response: %w", err)
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("token endpoint returned empty access_token")
	}

	f.accessToken = tok.AccessToken
	f.tokenExpiry = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	return f.accessToken, nil
}

// loadServiceAccount parses the JSON credentials and the RSA private
// key. Runs once; subsequent calls reuse the parsed result.
func (f *FCMPusher) loadServiceAccount() error {
	f.saOnce.Do(func() {
		var sa fcmServiceAccount
		if err := json.Unmarshal([]byte(f.serviceAccountJSON), &sa); err != nil {
			f.saErr = fmt.Errorf("parse service account JSON: %w", err)
			return
		}
		if sa.ClientEmail == "" || sa.PrivateKey == "" {
			f.saErr = fmt.Errorf("service account missing client_email or private_key")
			return
		}
		block, _ := pem.Decode([]byte(sa.PrivateKey))
		if block == nil {
			f.saErr = fmt.Errorf("service account private_key is not PEM-encoded")
			return
		}
		// PKCS#8 is what Google emits. Fall back to PKCS#1 for safety.
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			rsaKey, perr := x509.ParsePKCS1PrivateKey(block.Bytes)
			if perr != nil {
				f.saErr = fmt.Errorf("parse private key (pkcs8=%v, pkcs1=%v)", err, perr)
				return
			}
			sa.parsedKey = rsaKey
		} else {
			rsaKey, ok := key.(*rsa.PrivateKey)
			if !ok {
				f.saErr = fmt.Errorf("service account key is not RSA")
				return
			}
			sa.parsedKey = rsaKey
		}
		f.sa = &sa
	})
	return f.saErr
}

// buildSignedJWT constructs and RS256-signs a JWT assertion for the
// Google OAuth2 jwt-bearer flow. Scope is firebase.messaging which is
// what FCM v1 send requires.
func (f *FCMPusher) buildSignedJWT() (string, error) {
	now := time.Now()
	header := map[string]string{
		"alg": "RS256",
		"typ": "JWT",
		"kid": f.sa.PrivateKeyID,
	}
	claims := map[string]any{
		"iss":   f.sa.ClientEmail,
		"scope": "https://www.googleapis.com/auth/firebase.messaging",
		"aud":   "https://oauth2.googleapis.com/token",
		"iat":   now.Unix(),
		"exp":   now.Add(60 * time.Minute).Unix(),
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

	sum := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.sa.parsedKey, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signingInput + "." + enc.EncodeToString(sig), nil
}

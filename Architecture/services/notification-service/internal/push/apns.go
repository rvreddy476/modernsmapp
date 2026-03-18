package push

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"time"
)

// APNSPusher sends push notifications via Apple Push Notification Service.
type APNSPusher struct {
	teamID     string
	keyID      string
	privateKey *ecdsa.PrivateKey
	bundleID   string
	httpClient *http.Client
	production bool
}

// NewAPNSPusher creates an APNs pusher. privateKeyPEM is the .p8 file contents.
func NewAPNSPusher(teamID, keyID, privateKeyPEM, bundleID string, production bool) (*APNSPusher, error) {
	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil {
		return nil, fmt.Errorf("apns: failed to decode PEM block")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("apns: failed to parse private key: %w", err)
	}
	ecKey, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("apns: key is not ECDSA")
	}
	return &APNSPusher{
		teamID: teamID, keyID: keyID,
		privateKey: ecKey, bundleID: bundleID,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		production: production,
	}, nil
}

// buildJWT creates a signed ES256 JWT for APNs authentication.
func (a *APNSPusher) buildJWT() (string, error) {
	header := base64.RawURLEncoding.EncodeToString(mustJSON(map[string]string{
		"alg": "ES256",
		"kid": a.keyID,
	}))
	claims := base64.RawURLEncoding.EncodeToString(mustJSON(map[string]interface{}{
		"iss": a.teamID,
		"iat": time.Now().Unix(),
	}))
	signingInput := header + "." + claims
	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, a.privateKey, digest[:])
	if err != nil {
		return "", err
	}
	// Encode R and S as fixed-size 32-byte big-endian values (ES256 signature format)
	sig := make([]byte, 64)
	rBytes := r.Bytes()
	sBytes := s.Bytes()
	copy(sig[32-len(rBytes):32], rBytes)
	copy(sig[64-len(sBytes):64], sBytes)
	tokenStr := signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
	return tokenStr, nil
}

func mustJSON(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}

func (a *APNSPusher) Send(ctx context.Context, token, _, title, body string, data map[string]string) error {
	if a.privateKey == nil {
		return nil // APNs not configured
	}

	tokenStr, err := a.buildJWT()
	if err != nil {
		return err
	}

	aps := map[string]interface{}{
		"alert": map[string]string{"title": title, "body": body},
		"sound": "default",
	}

	// Apply collapse key as APNs thread-id for notification grouping.
	collapseKey := data["collapse_key"]
	if collapseKey != "" {
		aps["thread-id"] = collapseKey
	}

	payload := map[string]interface{}{
		"aps": aps,
	}
	for k, v := range data {
		if k == "collapse_key" {
			continue // already applied as thread-id
		}
		payload[k] = v
	}
	b, _ := json.Marshal(payload)

	host := "https://api.sandbox.push.apple.com"
	if a.production {
		host = "https://api.push.apple.com"
	}
	url := fmt.Sprintf("%s/3/device/%s", host, token)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	req.Header.Set("Authorization", "bearer "+tokenStr)
	req.Header.Set("apns-topic", a.bundleID)
	req.Header.Set("Content-Type", "application/json")

	// Set apns-collapse-id header — APNs uses this to replace notifications on the device.
	if collapseKey != "" {
		req.Header.Set("apns-collapse-id", collapseKey)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("apns: send failed with status %d", resp.StatusCode)
	}
	return nil
}

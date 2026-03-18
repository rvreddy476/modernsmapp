package push

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// FCMPusher sends push notifications via Firebase Cloud Messaging HTTP v1 API.
type FCMPusher struct {
	projectID          string
	serviceAccountJSON string // JSON key file contents
	httpClient         *http.Client
}

// NewFCMPusher creates an FCM pusher. serviceAccountJSON is the Firebase service account JSON.
func NewFCMPusher(projectID, serviceAccountJSON string) *FCMPusher {
	return &FCMPusher{
		projectID:          projectID,
		serviceAccountJSON: serviceAccountJSON,
		httpClient:         &http.Client{Timeout: 10 * time.Second},
	}
}

// Send delivers a push notification via FCM to an Android or Web device.
func (f *FCMPusher) Send(ctx context.Context, token, platform, title, body string, data map[string]string) error {
	if f.projectID == "" || f.serviceAccountJSON == "" {
		return nil // FCM not configured, skip silently
	}

	msg := map[string]interface{}{
		"token": token,
		"notification": map[string]string{
			"title": title,
			"body":  body,
		},
		"data": data,
	}

	// Apply collapse key if provided in data.
	// FCM collapse_key causes the latest notification to replace older ones with the same key.
	if ck, ok := data["collapse_key"]; ok && ck != "" {
		msg["android"] = map[string]interface{}{
			"collapse_key": ck,
		}
	}

	payload := map[string]interface{}{
		"message": msg,
	}

	b, _ := json.Marshal(payload)
	url := fmt.Sprintf("https://fcm.googleapis.com/v1/projects/%s/messages:send", f.projectID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	// NOTE: In production, use OAuth2 token from service account JSON.
	// For now, accept GOOGLE_ACCESS_TOKEN env variable as bearer token.
	// Full OAuth2 implementation requires golang.org/x/oauth2/google.
	req.Header.Set("Authorization", "Bearer "+getAccessToken(f.serviceAccountJSON))

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("fcm: send failed with status %d", resp.StatusCode)
	}
	return nil
}

func getAccessToken(serviceAccountJSON string) string {
	// Placeholder: in production use golang.org/x/oauth2/google to get access token
	// from the service account JSON credentials.
	return ""
}

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/google/uuid"
)

type MiniAppSessionIssuer interface {
	IssueMiniAppSession(ctx context.Context, appID, userID uuid.UUID, grantedPermissions []string) (*MiniAppSession, error)
}

type AuthClient struct {
	baseURL     string
	internalKey string
	httpClient  *http.Client
}

func NewAuthClient(baseURL, internalKey string) *AuthClient {
	return &AuthClient{
		baseURL:     baseURL,
		internalKey: internalKey,
		httpClient:  &http.Client{},
	}
}

type issueMiniAppSessionRequest struct {
	AppID              string   `json:"app_id"`
	UserID             string   `json:"user_id"`
	GrantedPermissions []string `json:"granted_permissions"`
}

type responseEnvelope struct {
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c *AuthClient) IssueMiniAppSession(ctx context.Context, appID, userID uuid.UUID, grantedPermissions []string) (*MiniAppSession, error) {
	payload, err := json.Marshal(issueMiniAppSessionRequest{
		AppID:              appID.String(),
		UserID:             userID.String(),
		GrantedPermissions: grantedPermissions,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+"/v1/auth/internal/mini-app-session",
		bytes.NewReader(payload),
	)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", c.internalKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var envelope responseEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		if envelope.Error != nil {
			switch envelope.Error.Code {
			case "SESSION_UNAVAILABLE", "JWKS_UNAVAILABLE", "INTERNAL_KEY_UNAVAILABLE", "UNAUTHORIZED":
				return nil, fmt.Errorf("SESSION_UNAVAILABLE")
			default:
				return nil, fmt.Errorf("%s", envelope.Error.Code)
			}
		}
		return nil, fmt.Errorf("AUTH_SERVICE_ERROR")
	}

	var session MiniAppSession
	if err := json.Unmarshal(envelope.Data, &session); err != nil {
		return nil, fmt.Errorf("decode session: %w", err)
	}
	return &session, nil
}

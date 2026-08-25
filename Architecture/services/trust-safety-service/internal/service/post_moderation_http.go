package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/shared/moderationcap"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

type HTTPPostModerationClient struct {
	baseURL     string
	internalKey string
	httpClient  *http.Client
	signer      *moderationcap.Signer
}

func NewHTTPPostModerationClient(baseURL, internalKey string, signer *moderationcap.Signer, client *http.Client) *HTTPPostModerationClient {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &HTTPPostModerationClient{
		baseURL: strings.TrimRight(baseURL, "/"), internalKey: internalKey,
		httpClient: client, signer: signer,
	}
}

func (c *HTTPPostModerationClient) GetSubject(ctx context.Context, postID uuid.UUID) (*PostModerationSubject, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/v1/posts/internal/moderation-subject/"+postID.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Internal-Service-Key", c.internalKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("post moderation subject returned %d", resp.StatusCode)
	}
	var envelope struct {
		Data struct {
			PostID          uuid.UUID `json:"post_id"`
			AuthorID        uuid.UUID `json:"author_id"`
			ReviewStatus    string    `json:"review_status"`
			ContentRevision int64     `json:"content_revision"`
			Deleted         bool      `json:"deleted"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, err
	}
	if envelope.Data.PostID != postID || envelope.Data.AuthorID == uuid.Nil || envelope.Data.ContentRevision <= 0 {
		return nil, fmt.Errorf("post moderation subject response is incomplete")
	}
	return &PostModerationSubject{
		PostID: envelope.Data.PostID, AuthorID: envelope.Data.AuthorID,
		ReviewStatus: envelope.Data.ReviewStatus, ContentRevision: envelope.Data.ContentRevision,
		Deleted: envelope.Data.Deleted,
	}, nil
}

func (c *HTTPPostModerationClient) OverturnAppeal(ctx context.Context, appeal *postgres.ContentAppeal, reviewerID uuid.UUID, contentRevision int64, reason string) error {
	claims, capability, err := c.signer.Sign(moderationcap.Claims{
		SubjectID: appeal.ContentID.String(), ContentRevision: contentRevision,
		Decision: "approve", Reason: reason, DecisionID: appeal.ID.String(),
		PolicyVersion: "appeal-v1", ActorID: reviewerID.String(),
	})
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{"claims": claims, "capability": capability})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/v1/posts/internal/moderation", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", c.internalKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("post moderation command returned %d", resp.StatusCode)
	}
	return nil
}

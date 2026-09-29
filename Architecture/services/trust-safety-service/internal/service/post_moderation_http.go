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

// moderationSubjectWire is GET /v1/posts/internal/moderation-subject/:id.
// post_id, author_id, review_status, content_revision and deleted are what
// post-service exposes today. The decision fields are read when present:
// last_decision_id / last_decision_source (with the plan's
// latest_base_decision_id spelling accepted for the id) and
// base_review_status over review_status once section 6.2 lands. Absent, an
// appeal binds by revision and base status, and no decision is copyright.
type moderationSubjectWire struct {
	PostID                uuid.UUID  `json:"post_id"`
	AuthorID              uuid.UUID  `json:"author_id"`
	ReviewStatus          string     `json:"review_status"`
	BaseReviewStatus      string     `json:"base_review_status"`
	ContentRevision       int64      `json:"content_revision"`
	Deleted               bool       `json:"deleted"`
	LastDecisionID        *uuid.UUID `json:"last_decision_id"`
	LatestBaseDecisionID  *uuid.UUID `json:"latest_base_decision_id"`
	LastDecisionSource    string     `json:"last_decision_source"`
	LatestBaseDecisionSrc string     `json:"latest_base_decision_source"`
}

func (w moderationSubjectWire) subject() *PostModerationSubject {
	s := &PostModerationSubject{
		PostID: w.PostID, AuthorID: w.AuthorID,
		ReviewStatus: w.ReviewStatus, ContentRevision: w.ContentRevision, Deleted: w.Deleted,
		LastDecisionID: w.LastDecisionID, LastDecisionSource: strings.TrimSpace(w.LastDecisionSource),
	}
	if strings.TrimSpace(w.BaseReviewStatus) != "" {
		s.ReviewStatus = strings.TrimSpace(w.BaseReviewStatus)
	}
	if s.LastDecisionID == nil {
		s.LastDecisionID = w.LatestBaseDecisionID
	}
	if s.LastDecisionSource == "" {
		s.LastDecisionSource = strings.TrimSpace(w.LatestBaseDecisionSrc)
	}
	if s.LastDecisionID != nil && *s.LastDecisionID == uuid.Nil {
		s.LastDecisionID = nil
	}
	return s
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
	if resp.StatusCode == http.StatusNotFound {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, ErrPostSubjectNotFound
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("post moderation subject returned %d", resp.StatusCode)
	}
	var envelope struct {
		Data moderationSubjectWire `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, err
	}
	subject := envelope.Data.subject()
	if subject.PostID != postID || subject.AuthorID == uuid.Nil || subject.ContentRevision <= 0 {
		return nil, fmt.Errorf("post moderation subject response is incomplete")
	}
	return subject, nil
}

// OverturnAppeal signs the approve under decision id appeal.ID with the
// revision the caller captured, and maps post-service's refusals:
// STALE_MODERATION_SUBJECT and INVALID_TRANSITION are
// ErrPostDecisionSuperseded (the post moved on), DECISION_CONFLICT is
// ErrPostDecisionConflict (the id was used with other claims). Anything
// else is a transient failure the caller may retry with the same claims.
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
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	code := errorCodeOf(resp.Body)
	if resp.StatusCode == http.StatusConflict {
		switch code {
		case "STALE_MODERATION_SUBJECT", "INVALID_TRANSITION", "SUPERSEDED":
			return fmt.Errorf("%w (%s)", ErrPostDecisionSuperseded, code)
		case "DECISION_CONFLICT":
			return ErrPostDecisionConflict
		}
	}
	return fmt.Errorf("post moderation command returned %d %s", resp.StatusCode, code)
}

// errorCodeOf reads the error code from an api error envelope; "" when
// the body is not one. At most 64 KiB is read.
func errorCodeOf(body io.Reader) string {
	var env struct {
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(body, 64<<10)).Decode(&env); err != nil || env.Error == nil {
		return ""
	}
	return env.Error.Code
}

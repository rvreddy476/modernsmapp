package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/shared/moderationcap"
	"github.com/atpost/shared/servicetoken"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

// postModerationOp is the service-token scope post-service requires on
// POST /v1/posts/internal/moderation (security sweep #11, 21 Oct 2026: the
// gateway stamps the shared key on every proxied request, so the key no
// longer identifies the caller). The token is minted with the same
// TRUST_SAFETY_SERVICE_TOKEN_KEY/KID as the restriction routes.
const postModerationOp = "post:moderation.write"

// ErrPostModerationNotConfigured: no service-token signer, so the command
// cannot be authenticated. Transient from the caller's point of view: the
// appeal stays in flight and the sweeper replays it once the key is set.
var ErrPostModerationNotConfigured = errors.New("post moderation client has no service-token signer")

type HTTPPostModerationClient struct {
	baseURL     string
	internalKey string
	httpClient  *http.Client
	signer      *moderationcap.Signer
	tokens      *servicetoken.Signer
}

// NewHTTPPostModerationClient builds the client. tokens mints the
// post:moderation.write token every command carries; nil leaves commands
// refused with ErrPostModerationNotConfigured (subject reads still work).
func NewHTTPPostModerationClient(baseURL, internalKey string, signer *moderationcap.Signer, tokens *servicetoken.Signer, client *http.Client) *HTTPPostModerationClient {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &HTTPPostModerationClient{
		baseURL: strings.TrimRight(baseURL, "/"), internalKey: internalKey,
		httpClient: client, signer: signer, tokens: tokens,
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
	status, code, err := c.command(ctx, "appeal-overturn", moderationcap.Claims{
		SubjectID: appeal.ContentID.String(), ContentRevision: contentRevision,
		Decision: "approve", Reason: reason, DecisionID: appeal.ID.String(),
		PolicyVersion: "appeal-v1", ActorID: reviewerID.String(),
	})
	if err != nil || status/100 == 2 {
		return err
	}
	if status == http.StatusConflict {
		switch code {
		case "STALE_MODERATION_SUBJECT", "INVALID_TRANSITION", "SUPERSEDED":
			return fmt.Errorf("%w (%s)", ErrPostDecisionSuperseded, code)
		case "DECISION_CONFLICT":
			return ErrPostDecisionConflict
		}
	}
	return fmt.Errorf("post moderation command returned %d %s", status, code)
}

// reportPolicyVersion marks a decision as an upheld report to post-service
// (its reportPolicyPrefix "report-"): recorded with source "report", and
// only a reject is accepted from it.
const reportPolicyVersion = "report-v1"

// RejectReportedPost sends the reject an upheld report owes (reels-29):
// decision id = the report id, so a replay is the same decision. The
// refusals map for the takedown sender: STALE_MODERATION_SUBJECT is
// ErrPostSubjectStale (read the revision again and retry);
// INVALID_TRANSITION / SUPERSEDED are ErrPostDecisionSuperseded (nothing to
// take down); DECISION_CONFLICT is ErrPostDecisionConflict and a 400 is
// ErrPostDecisionRefused (both final). Anything else is transient.
func (c *HTTPPostModerationClient) RejectReportedPost(ctx context.Context, reportID, postID, moderatorID uuid.UUID, contentRevision int64, reason string) error {
	status, code, err := c.command(ctx, "report-takedown", moderationcap.Claims{
		SubjectID: postID.String(), ContentRevision: contentRevision,
		Decision: "reject", Reason: reason, DecisionID: reportID.String(),
		PolicyVersion: reportPolicyVersion, ActorID: moderatorID.String(),
	})
	if err != nil || status/100 == 2 {
		return err
	}
	switch {
	case status == http.StatusConflict && code == "STALE_MODERATION_SUBJECT":
		return fmt.Errorf("%w (%s)", ErrPostSubjectStale, code)
	case status == http.StatusConflict && (code == "INVALID_TRANSITION" || code == "SUPERSEDED"):
		return fmt.Errorf("%w (%s)", ErrPostDecisionSuperseded, code)
	case status == http.StatusConflict && code == "DECISION_CONFLICT":
		return ErrPostDecisionConflict
	case status == http.StatusBadRequest:
		return fmt.Errorf("%w (%s)", ErrPostDecisionRefused, code)
	}
	return fmt.Errorf("post moderation command returned %d %s", status, code)
}

// command signs claims, sends them with a post:moderation.write token
// (purpose names the token's use) and returns post-service's status and
// error code. err is a failure to send at all.
func (c *HTTPPostModerationClient) command(ctx context.Context, purpose string, cl moderationcap.Claims) (int, string, error) {
	if c.tokens == nil {
		return 0, "", ErrPostModerationNotConfigured
	}
	tok, err := c.tokens.Mint("post", purpose, []string{postModerationOp}, nil, time.Minute)
	if err != nil {
		return 0, "", err
	}
	claims, capability, err := c.signer.Sign(cl)
	if err != nil {
		return 0, "", err
	}
	body, err := json.Marshal(map[string]any{"claims": claims, "capability": capability})
	if err != nil {
		return 0, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/v1/posts/internal/moderation", bytes.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Service-Authorization", "Bearer "+tok)
	req.Header.Set("X-Internal-Service-Key", c.internalKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 == 2 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode, "", nil
	}
	return resp.StatusCode, errorCodeOf(resp.Body), nil
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

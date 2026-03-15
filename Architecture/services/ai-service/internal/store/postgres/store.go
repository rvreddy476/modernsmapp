package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AIJob represents a row in ai.ai_jobs.
type AIJob struct {
	ID           uuid.UUID       `json:"id"`
	JobType      string          `json:"job_type"`
	InputRefType string          `json:"input_ref_type"`
	InputRefID   uuid.UUID       `json:"input_ref_id"`
	RequesterID  *uuid.UUID      `json:"requester_id,omitempty"`
	Status       string          `json:"status"`
	Result       json.RawMessage `json:"result,omitempty"`
	ErrorMessage *string         `json:"error_message,omitempty"`
	ModelVersion *string         `json:"model_version,omitempty"`
	LatencyMs    *int            `json:"latency_ms,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	CompletedAt  *time.Time      `json:"completed_at,omitempty"`
}

// ModerationResult represents a row in ai.moderation_ai_results.
type ModerationResult struct {
	ID           uuid.UUID  `json:"id"`
	ContentType  string     `json:"content_type"`
	ContentID    uuid.UUID  `json:"content_id"`
	TextScore    *float32   `json:"text_score,omitempty"`
	ImageScore   *float32   `json:"image_score,omitempty"`
	Flags        []string   `json:"flags,omitempty"`
	Action       string     `json:"action"`
	ModelVersion *string    `json:"model_version,omitempty"`
	CheckedAt    time.Time  `json:"checked_at"`
}

// Store provides data access for the ai-service.
type Store struct {
	db *pgxpool.Pool
}

// New returns a new Store.
func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

// CreateJob inserts a new AI job record.
func (s *Store) CreateJob(ctx context.Context, job *AIJob) error {
	const q = `
		INSERT INTO ai.ai_jobs
			(id, job_type, input_ref_type, input_ref_id, requester_id, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`

	if job.ID == uuid.Nil {
		job.ID = uuid.New()
	}
	job.CreatedAt = time.Now().UTC()
	job.Status = "queued"

	_, err := s.db.Exec(ctx, q,
		job.ID, job.JobType, job.InputRefType, job.InputRefID,
		job.RequesterID, job.Status, job.CreatedAt,
	)
	if err != nil {
		slog.Error("store.CreateJob failed", "error", err)
		return fmt.Errorf("create job: %w", err)
	}
	return nil
}

// GetJob fetches a single job by its ID.
func (s *Store) GetJob(ctx context.Context, id uuid.UUID) (*AIJob, error) {
	const q = `
		SELECT id, job_type, input_ref_type, input_ref_id, requester_id,
		       status, result, error_message, model_version, latency_ms,
		       created_at, completed_at
		FROM ai.ai_jobs WHERE id = $1`

	row := s.db.QueryRow(ctx, q, id)
	job, err := scanJob(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		slog.Error("store.GetJob failed", "error", err, "id", id)
		return nil, fmt.Errorf("get job: %w", err)
	}
	return job, nil
}

// UpdateJobStatus updates a job's status, result, error, and latency.
func (s *Store) UpdateJobStatus(ctx context.Context, id uuid.UUID, status string, result []byte, errMsg string, latencyMs int) error {
	const q = `
		UPDATE ai.ai_jobs
		SET status = $2, result = $3, error_message = $4,
		    latency_ms = $5, completed_at = NOW()
		WHERE id = $1`

	var resultArg interface{}
	if len(result) > 0 {
		resultArg = result
	}
	var errMsgArg interface{}
	if errMsg != "" {
		errMsgArg = errMsg
	}
	var latencyArg interface{}
	if latencyMs > 0 {
		latencyArg = latencyMs
	}

	_, err := s.db.Exec(ctx, q, id, status, resultArg, errMsgArg, latencyArg)
	if err != nil {
		slog.Error("store.UpdateJobStatus failed", "error", err, "id", id)
		return fmt.Errorf("update job status: %w", err)
	}
	return nil
}

// ListJobsByRef returns jobs for a given ref type/id ordered by recency.
func (s *Store) ListJobsByRef(ctx context.Context, refType string, refID uuid.UUID, limit int) ([]AIJob, error) {
	const q = `
		SELECT id, job_type, input_ref_type, input_ref_id, requester_id,
		       status, result, error_message, model_version, latency_ms,
		       created_at, completed_at
		FROM ai.ai_jobs
		WHERE input_ref_type = $1 AND input_ref_id = $2
		ORDER BY created_at DESC
		LIMIT $3`

	rows, err := s.db.Query(ctx, q, refType, refID, limit)
	if err != nil {
		return nil, fmt.Errorf("list jobs by ref: %w", err)
	}
	defer rows.Close()

	var jobs []AIJob
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scan job: %w", err)
		}
		jobs = append(jobs, *job)
	}
	return jobs, rows.Err()
}

// CreateModerationResult upserts a moderation result.
func (s *Store) CreateModerationResult(ctx context.Context, r *ModerationResult) error {
	const q = `
		INSERT INTO ai.moderation_ai_results
			(id, content_type, content_id, text_score, image_score, flags, action, model_version, checked_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (content_type, content_id) DO UPDATE
			SET text_score    = EXCLUDED.text_score,
			    image_score   = EXCLUDED.image_score,
			    flags         = EXCLUDED.flags,
			    action        = EXCLUDED.action,
			    model_version = EXCLUDED.model_version,
			    checked_at    = EXCLUDED.checked_at`

	if r.ID == uuid.Nil {
		r.ID = uuid.New()
	}
	r.CheckedAt = time.Now().UTC()

	_, err := s.db.Exec(ctx, q,
		r.ID, r.ContentType, r.ContentID,
		r.TextScore, r.ImageScore, r.Flags,
		r.Action, r.ModelVersion, r.CheckedAt,
	)
	if err != nil {
		slog.Error("store.CreateModerationResult failed", "error", err)
		return fmt.Errorf("create moderation result: %w", err)
	}
	return nil
}

// GetModerationResult fetches the latest moderation result for a piece of content.
func (s *Store) GetModerationResult(ctx context.Context, contentType string, contentID uuid.UUID) (*ModerationResult, error) {
	const q = `
		SELECT id, content_type, content_id, text_score, image_score,
		       flags, action, model_version, checked_at
		FROM ai.moderation_ai_results
		WHERE content_type = $1 AND content_id = $2`

	row := s.db.QueryRow(ctx, q, contentType, contentID)
	r := &ModerationResult{}
	err := row.Scan(
		&r.ID, &r.ContentType, &r.ContentID,
		&r.TextScore, &r.ImageScore, &r.Flags,
		&r.Action, &r.ModelVersion, &r.CheckedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		slog.Error("store.GetModerationResult failed", "error", err)
		return nil, fmt.Errorf("get moderation result: %w", err)
	}
	return r, nil
}

// scanJob is a helper that scans a job row from any pgx row-like type.
type scannable interface {
	Scan(dest ...any) error
}

func scanJob(row scannable) (*AIJob, error) {
	j := &AIJob{}
	err := row.Scan(
		&j.ID, &j.JobType, &j.InputRefType, &j.InputRefID, &j.RequesterID,
		&j.Status, &j.Result, &j.ErrorMessage, &j.ModelVersion, &j.LatencyMs,
		&j.CreatedAt, &j.CompletedAt,
	)
	if err != nil {
		return nil, err
	}
	return j, nil
}

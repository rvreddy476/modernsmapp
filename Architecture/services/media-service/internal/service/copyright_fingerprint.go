package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Internal fingerprint routes (Copyright Match phase 1). Service-to-service
// only; the handler sits behind the internal key. Nothing here creates a
// URL or names a user.

// ErrFingerprintNotReady re-exports the store's refusal.
var ErrFingerprintNotReady = postgres.ErrFingerprintNotReady

// FingerprintJobView is one job row on the wire.
type FingerprintJobView struct {
	MediaGeneration int64      `json:"media_generation"`
	AlgoVersion     int        `json:"algo_version"`
	Priority        int        `json:"priority"`
	Status          string     `json:"status"`
	SkipReason      *string    `json:"skip_reason,omitempty"`
	Attempts        int        `json:"attempts"`
	HeartbeatAt     *time.Time `json:"heartbeat_at,omitempty"`
	NotBefore       time.Time  `json:"not_before"`
	ProgressMs      int        `json:"progress_ms"`
	Truncation      any        `json:"truncation,omitempty"`
	LastError       *string    `json:"last_error,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// FingerprintStatus is GET /v1/media/internal/:id/fingerprint.
type FingerprintStatus struct {
	MediaID          uuid.UUID            `json:"media_id"`
	MediaGeneration  int64                `json:"media_generation"`
	ReadyGeneration  *int64               `json:"ready_generation,omitempty"`
	ProcessingStatus string               `json:"processing_status"`
	UploadConfirmed  *time.Time           `json:"upload_confirmed_at,omitempty"`
	UploadTimeSource string               `json:"upload_time_source,omitempty"`
	Fingerprinted    bool                 `json:"fingerprinted"`
	InputKind        string               `json:"input_kind,omitempty"`
	InputRef         string               `json:"input_ref,omitempty"`
	InformativeMs    int                  `json:"informative_ms,omitempty"`
	Jobs             []FingerprintJobView `json:"jobs"`
}

// EnqueueFingerprintResult is POST /v1/media/internal/:id/fingerprint.
type EnqueueFingerprintResult struct {
	MediaID uuid.UUID          `json:"media_id"`
	Created bool               `json:"created"`
	Job     FingerprintJobView `json:"job"`
}

func jobView(j postgres.FingerprintJob) FingerprintJobView {
	v := FingerprintJobView{
		MediaGeneration: j.Generation, AlgoVersion: j.Algo, Priority: j.Priority, Status: j.Status,
		SkipReason: j.SkipReason, Attempts: j.Attempts, HeartbeatAt: j.HeartbeatAt, NotBefore: j.NotBefore,
		ProgressMs: j.ProgressMs, LastError: j.LastError, CreatedAt: j.CreatedAt, UpdatedAt: j.UpdatedAt,
	}
	if len(j.Truncation) > 0 {
		v.Truncation = j.Truncation
	}
	return v
}

// EnqueueFingerprint queues (or re-queues a failed/skipped) fingerprint job
// for a ready video's current generation, at upload priority.
func (s *Service) EnqueueFingerprint(ctx context.Context, mediaID uuid.UUID, retry bool) (*EnqueueFingerprintResult, error) {
	job, created, err := s.pgStore.EnqueueFingerprintJob(ctx, mediaID, postgres.FingerprintPriorityUpload, retry)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrAssetNotFound
	}
	if err != nil {
		return nil, err
	}
	return &EnqueueFingerprintResult{MediaID: mediaID, Created: created, Job: jobView(*job)}, nil
}

// GetFingerprintStatus reads the asset's generation state and job rows.
func (s *Service) GetFingerprintStatus(ctx context.Context, mediaID uuid.UUID) (*FingerprintStatus, error) {
	st, err := s.pgStore.GetMediaGenerationState(ctx, mediaID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrAssetNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("fingerprint status for %s: %w", mediaID, err)
	}
	out := &FingerprintStatus{
		MediaID: mediaID, MediaGeneration: st.MediaGeneration, ReadyGeneration: st.ReadyGeneration,
		ProcessingStatus: st.ProcessingStatus, UploadConfirmed: st.UploadConfirmed, UploadTimeSource: st.UploadTimeSource,
		Jobs: []FingerprintJobView{},
	}
	if st.ReadyGeneration != nil {
		fp, err := s.pgStore.LoadFingerprint(ctx, mediaID, *st.ReadyGeneration, postgres.FingerprintAlgoVersion)
		if err != nil {
			return nil, err
		}
		if fp != nil && fp.SupersededAt == nil {
			out.Fingerprinted = true
			out.InputKind, out.InputRef, out.InformativeMs = fp.InputKind, fp.InputRef, fp.InformativeMs
		}
	}
	jobs, err := s.pgStore.GetFingerprintJobs(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	for _, j := range jobs {
		out.Jobs = append(out.Jobs, jobView(j))
	}
	return out, nil
}

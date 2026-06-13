package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/food-service/internal/store/blob"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/google/uuid"
)

// settlementPresignTTL is how long a download URL stays valid. 15 min
// is generous for a single-CSV admin click + retry, short enough that
// a leaked URL doesn't grant indefinite access.
const settlementPresignTTL = 15 * time.Minute

// WithBlobStore wires the MinIO client. Optional — when nil,
// settlement generation + download stay on the inline-body fallback.
// main.go can degrade safely if MinIO is unreachable at startup.
func (s *Service) WithBlobStore(b *blob.Store) *Service {
	s.blob = b
	return s
}

// GenerateSettlementFile builds the CSV for the requested kind +
// period, inserts the audit row in food.settlement_files, and (when
// MinIO is wired) uploads the CSV. On upload success the inline body
// is cleared and file_url points at the MinIO key; on failure the
// row stays inline-only so the download endpoint can still serve it.
func (s *Service) GenerateSettlementFile(ctx context.Context, adminID uuid.UUID, kind string, from, to time.Time) (*postgres.SettlementFile, error) {
	if to.Before(from) {
		return nil, fmt.Errorf("invalid: to before from")
	}
	var (
		f   *postgres.SettlementFile
		err error
	)
	switch kind {
	case "restaurant":
		f, err = s.store.GenerateRestaurantSettlementFile(ctx, adminID, from, to)
	case "delivery":
		f, err = s.store.GenerateDeliverySettlementFile(ctx, adminID, from, to)
	default:
		return nil, fmt.Errorf("invalid kind: %s", kind)
	}
	if err != nil {
		return nil, err
	}
	// Best-effort MinIO offload. Inline body stays in the DB row
	// until UpdateSettlementFileURL nulls it on a successful upload.
	if s.blob != nil {
		key := fmt.Sprintf("settlements/%s/%s-%s-%s.csv",
			f.Kind,
			from.Format("2006-01-02"),
			to.Format("2006-01-02"),
			f.ID.String(),
		)
		body, berr := s.store.GetSettlementBody(ctx, f.ID)
		if berr == nil && len(body.InlineBody) > 0 {
			if uerr := s.blob.Upload(ctx, key, body.InlineBody, "text/csv"); uerr == nil {
				if err := s.store.UpdateSettlementFileURL(ctx, f.ID, key); err != nil {
					slog.Warn("food-service: settlement file_url update failed",
						"file_id", f.ID, "error", err)
				} else {
					f.FileURL = key
				}
			} else {
				slog.Warn("food-service: MinIO upload failed, keeping inline body",
					"file_id", f.ID, "error", uerr)
			}
		}
	}
	return f, nil
}

// ListSettlementFiles returns the audit log of generated files.
func (s *Service) ListSettlementFiles(ctx context.Context, limit int) ([]postgres.SettlementFile, error) {
	return s.store.ListSettlementFiles(ctx, limit)
}

// GetSettlementFileBody is the legacy inline-only download path.
// Kept for backwards-compatibility; new callers should use
// GetSettlementDownload which prefers a presigned URL.
func (s *Service) GetSettlementFileBody(ctx context.Context, fileID uuid.UUID) ([]byte, string, error) {
	return s.store.GetSettlementFileBody(ctx, fileID)
}

// SettlementDownload tells the handler how to deliver the CSV.
// When PresignedURL is set, redirect to it; otherwise stream
// InlineBody with text/csv. Kind drives the filename suffix.
type SettlementDownload struct {
	PresignedURL string
	InlineBody   []byte
	Kind         string
}

// GetSettlementDownload returns the right delivery mode for an admin
// download. Falls back to inline bytes when the row is pre-MinIO or
// the upload failed at generation time.
func (s *Service) GetSettlementDownload(ctx context.Context, fileID uuid.UUID) (*SettlementDownload, error) {
	body, err := s.store.GetSettlementBody(ctx, fileID)
	if err != nil {
		return nil, err
	}
	if body.FileURL != "" && s.blob != nil {
		url, perr := s.blob.PresignedGetURL(ctx, body.FileURL, settlementPresignTTL)
		if perr == nil {
			return &SettlementDownload{PresignedURL: url, Kind: body.Kind}, nil
		}
		slog.Warn("food-service: presign failed, falling back to inline",
			"file_id", fileID, "error", perr)
	}
	return &SettlementDownload{InlineBody: body.InlineBody, Kind: body.Kind}, nil
}

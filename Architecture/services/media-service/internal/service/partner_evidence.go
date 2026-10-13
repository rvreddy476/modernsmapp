package service

import (
	"context"
	"github.com/atpost/media-service/internal/processing"
	"github.com/google/uuid"
	"io"
	"strings"
)

const MaxIntroductionBytes = 64 << 20

// PartnerEvidence returns private review bytes, never a signed storage URL.
// The caller must be the admin service after resolving a module-owned document.
func (r *ImageBytesReader) PartnerEvidence(ctx context.Context, id, owner uuid.UUID, kind string) (*processing.DisplayImage, error) {
	m, err := r.records.GetMedia(ctx, id)
	if err != nil {
		return nil, err
	}
	if m == nil || owner == uuid.Nil || m.UploaderID != owner || m.StorageKey == "" || strings.HasPrefix(m.StorageKey, "public/") || m.UploadPurpose != "" || m.ProcessingStatus != "ready" || m.ModerationStatus != "passed" {
		return nil, ErrImageBytesNotFound
	}
	if kind != "introduction_video" {
		if m.FileType != "image" {
			return nil, ErrImageBytesNotFound
		}
		return r.DisplayImage(ctx, id)
	}
	if m.FileType != "video" || m.DurationMs == nil || *m.DurationMs <= 0 || *m.DurationMs > 60000 || m.FileSizeBytes <= 0 || m.FileSizeBytes > MaxIntroductionBytes {
		return nil, ErrImageBytesNotFound
	}
	if m.MimeType != "video/mp4" && m.MimeType != "video/webm" && m.MimeType != "video/quicktime" {
		return nil, ErrImageBytesNotFound
	}
	body, info, err := r.objects.OpenObject(ctx, m.StorageKey)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	if info.Size > MaxIntroductionBytes {
		return nil, ErrImageBytesNotFound
	}
	data, err := io.ReadAll(io.LimitReader(body, MaxIntroductionBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > MaxIntroductionBytes {
		return nil, ErrImageBytesNotFound
	}
	return &processing.DisplayImage{Bytes: data, ContentType: m.MimeType}, nil
}

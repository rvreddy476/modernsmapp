package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Module 1 P0-6 — voice post processing.
//
// At confirm time:
//  1. magic-byte check (ConfirmUpload) rejected spoofed containers
//  2. ffprobe the REAL duration and enforce ≤180 s server-side
//  3. generate a waveform for the player scrubber
//  4. request an async transcript through the existing captions backend
//     (P0-9); public distribution stays gated until baseline safety lands
//
// Captions live in media_subtitles — the same canonical store video uses —
// so every surface referencing the media shares one caption track.

// Voice safety states recorded on media_assets.moderation_status.
// "pending" blocks public distribution; "approved" releases it; "failed"
// routes to manual review rather than auto-approving.
const (
	VoiceSafetyPending  = "pending"
	VoiceSafetyApproved = "approved"
	// VoiceSafetyFailed = no verdict could be produced → manual review.
	VoiceSafetyFailed = "failed"
	// VoiceSafetyRejected = an evaluation ran and found the content unsafe.
	VoiceSafetyRejected = "rejected"
)

// processVoice runs the synchronous part of voice handling. It returns an
// error only when the upload must be rejected outright.
func (s *Service) processVoice(ctx context.Context, media *postgres.MediaAsset) error {
	data, err := s.blobStore.DownloadObject(ctx, media.StorageKey)
	if err != nil {
		// Can't verify duration ⇒ can't admit (fail-closed, same policy
		// as the image scanner path).
		_ = s.pgStore.UpdateStatus(ctx, media.ID, "rejected")
		return fmt.Errorf("voice upload rejected: unable to read uploaded audio: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "voice-"+media.ID.String())
	if err != nil {
		_ = s.pgStore.UpdateStatus(ctx, media.ID, "rejected")
		return fmt.Errorf("voice upload rejected: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	localPath := filepath.Join(tmpDir, "input"+audioExt(media.MimeType))
	if err := os.WriteFile(localPath, data, 0o600); err != nil {
		_ = s.pgStore.UpdateStatus(ctx, media.ID, "rejected")
		return fmt.Errorf("voice upload rejected: %w", err)
	}

	meta, err := processing.ProbeAudio(ctx, localPath)
	if err != nil || meta == nil {
		_ = s.pgStore.UpdateStatus(ctx, media.ID, "rejected")
		return fmt.Errorf("voice upload rejected: audio could not be decoded")
	}
	durationSec := float64(meta.DurationMs) / 1000.0
	if err := ValidateVoiceDuration(durationSec); err != nil {
		_ = s.pgStore.UpdateStatus(ctx, media.ID, "rejected")
		return fmt.Errorf("voice upload rejected: %w", err)
	}

	durationRounded := int(durationSec + 0.5)
	if err := s.pgStore.UpdateMediaDuration(ctx, media.ID, durationRounded); err != nil {
		slog.Warn("voice: failed to persist duration", "media_id", media.ID, "error", err)
	}
	media.DurationSeconds = &durationRounded

	// Waveform for the player scrubber — best-effort; a missing waveform
	// degrades to a plain progress bar, it never fails the upload.
	if wfPath, wfErr := processing.GenerateWaveform(ctx, localPath, tmpDir, 200); wfErr == nil {
		if wfData, readErr := os.ReadFile(wfPath); readErr == nil {
			wfKey := fmt.Sprintf("user/%s/%s/waveform.json", media.UploaderID, media.ID)
			if upErr := s.blobStore.UploadObject(ctx, wfKey, wfData, "application/json"); upErr != nil {
				slog.Warn("voice: waveform upload failed", "media_id", media.ID, "error", upErr)
			}
		}
	} else {
		slog.Info("voice: waveform generation skipped", "media_id", media.ID, "error", wfErr)
	}

	return nil
}

// audioExt maps a MIME type to a container extension so ffprobe/ffmpeg
// select the right demuxer.
func audioExt(mime string) string {
	switch mime {
	case "audio/mpeg":
		return ".mp3"
	case "audio/ogg", "audio/opus":
		return ".ogg"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	case "audio/webm":
		return ".webm"
	case "audio/flac":
		return ".flac"
	case "audio/amr":
		return ".amr"
	default:
		return ".m4a"
	}
}

// ErrInvalidCaption marks a malformed correction request (400).
var ErrInvalidCaption = errors.New("invalid caption")

// maxCaptionContentBytes bounds a stored transcript (~3 hours of speech).
const maxCaptionContentBytes = 200_000

// CorrectCaption stores an owner-authored transcript correction as the
// canonical caption content, owner-only (fixes-v2 / Codex P1-3).
//
// v1 had `UpdateSubtitleContent` in the store with NO caller, so
// `edited_by_owner` was never set in any application flow and the
// "auto-generation must not overwrite an owner correction" guard could
// never actually fire. This is that caller.
func (s *Service) CorrectCaption(ctx context.Context, mediaID, userID uuid.UUID, language, content string) (*CaptionStatus, error) {
	if err := s.AssertMediaOwner(ctx, mediaID, userID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("%w: transcript content is required", ErrInvalidCaption)
	}
	if len(content) > maxCaptionContentBytes {
		return nil, fmt.Errorf("%w: transcript exceeds %d characters",
			ErrInvalidCaption, maxCaptionContentBytes)
	}
	if language == "" {
		language = "en"
	}
	if !validLanguageTag(language) {
		return nil, fmt.Errorf("%w: language must be a BCP-47-like tag (e.g. en, hi, en-IN)", ErrInvalidCaption)
	}

	if err := s.pgStore.UpdateSubtitleContent(ctx, mediaID, language, content, true); err != nil {
		return nil, err
	}

	// Module 1 fixes-v3 / LB-2 — THE trust boundary.
	//
	// v2 called evaluateVoiceSafety(ctx, mediaID, content) here. That was
	// a moderation bypass: a creator could upload harmful audio, submit a
	// sanitized caption while the asset was still pending, and have the
	// keyword evaluator "approve" the asset based on the creator's own
	// text rather than on provider-generated transcription evidence.
	//
	// Owner corrections now improve the DISPLAY/accessibility caption
	// only. They are never moderation evidence, and this function no
	// longer touches any approval-capable path. The safety verdict comes
	// exclusively from provider-generated evidence (runCaptionJob →
	// evaluateVoiceSafety with the provider transcript) or from an
	// authorized human review decision.
	//
	// Note also that the media's moderation_status is deliberately NOT
	// read or written here: an owner edit must not move rejected/failed
	// toward approved, and must not reset an existing verdict.

	return &CaptionStatus{
		MediaID: mediaID, Status: "completed", Language: language,
		Source: "manual", Text: content,
		Backend: backendName(s), UpdatedAt: time.Now().UTC(),
	}, nil
}

// validLanguageTag accepts short BCP-47-ish tags: "en", "hi", "en-IN".
// The column is VARCHAR(10), so anything longer is rejected here rather
// than truncated by the database.
func validLanguageTag(tag string) bool {
	if len(tag) == 0 || len(tag) > 10 {
		return false
	}
	for i := 0; i < len(tag); i++ {
		c := tag[i]
		isAlpha := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		if !isAlpha && c != '-' {
			return false
		}
	}
	return true
}

// CaptionStatus is the client-facing caption/transcript state (P0-9).
//
// Status values:
//
//	unavailable — no real backend configured; the UI says so plainly and
//	              never renders a placeholder as a finished caption
//	pending     — requested, not yet produced
//	completed   — a real transcript exists
//	failed      — the backend errored; media and draft are untouched and
//	              the client can retry
type CaptionStatus struct {
	MediaID   uuid.UUID `json:"media_id"`
	Status    string    `json:"status"`
	Language  string    `json:"language,omitempty"`
	Text      string    `json:"text,omitempty"`
	Source    string    `json:"source,omitempty"` // auto | manual | translated
	Backend   string    `json:"backend,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CaptionsBackendConfigured reports whether a REAL transcription backend
// is wired (the stub does not count).
func (s *Service) CaptionsBackendConfigured() bool {
	return s.captions != nil && s.captions.Name() != "stub"
}

// GetCaptionStatus reports the caption state for a media asset.
//
// Content comes from the canonical media_subtitles store; request
// lifecycle comes from the durable media_caption_jobs row. Status is
// never inferred from "a provider exists" (Codex P0-2):
//
//	no backend configured        → unavailable
//	backend, but no request made → not_requested
//	job pending/running          → pending
//	subtitle row present         → completed (with text)
//	job failed                   → failed (retryable; media/draft intact)
func (s *Service) GetCaptionStatus(ctx context.Context, mediaID uuid.UUID) (*CaptionStatus, error) {
	subs, err := s.pgStore.GetSubtitles(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	for _, sub := range subs {
		// Any persisted track (auto_generated / manual / translated) is a
		// completed caption. 'auto' is accepted only as a legacy spelling.
		switch sub.Source {
		case "auto_generated", "manual", "translated", "auto":
			return &CaptionStatus{
				MediaID: mediaID, Status: "completed",
				Language: sub.Language, Source: sub.Source,
				Text:    sub.Content,
				Backend: backendName(s), UpdatedAt: sub.CreatedAt,
			}, nil
		}
	}

	if !s.CaptionsBackendConfigured() {
		return &CaptionStatus{MediaID: mediaID, Status: "unavailable", UpdatedAt: time.Now().UTC()}, nil
	}

	job, err := s.pgStore.GetCaptionJob(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	if job == nil {
		return &CaptionStatus{
			MediaID: mediaID, Status: "not_requested",
			Backend: backendName(s), UpdatedAt: time.Now().UTC(),
		}, nil
	}
	status := "pending"
	switch job.Status {
	case "failed":
		status = "failed"
	case "completed":
		// Job says done but no subtitle row exists — treat as failed so
		// the client can retry rather than waiting forever.
		status = "failed"
	}
	return &CaptionStatus{
		MediaID: mediaID, Status: status, Language: job.Language,
		Backend: backendName(s), UpdatedAt: job.UpdatedAt,
	}, nil
}

func backendName(s *Service) string {
	if s.captions == nil {
		return ""
	}
	return s.captions.Name()
}

// RequestCaptions enqueues a DURABLE caption request, owner-only. With no
// real backend it returns "unavailable" without writing anything — a
// placeholder is never stored as a completed caption (Codex P0-9).
//
// The request is persisted rather than executed inline: the worker drains
// it, so a crash mid-transcription resumes instead of vanishing
// (Codex P0-2: "replace the untracked goroutine with durable work").
func (s *Service) RequestCaptions(ctx context.Context, mediaID, userID uuid.UUID, language string) (*CaptionStatus, error) {
	if err := s.AssertMediaOwner(ctx, mediaID, userID); err != nil {
		return nil, err
	}
	media, err := s.pgStore.GetMedia(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	if media.FileType != "audio" && media.FileType != "video" {
		return nil, fmt.Errorf("captions are only available for audio and video")
	}
	if !s.CaptionsBackendConfigured() {
		return &CaptionStatus{MediaID: mediaID, Status: "unavailable", UpdatedAt: time.Now().UTC()}, nil
	}
	if err := s.pgStore.EnqueueCaptionJob(ctx, mediaID, language, userID); err != nil {
		return nil, err
	}
	return &CaptionStatus{
		MediaID: mediaID, Status: "pending", Language: language,
		Backend: backendName(s), UpdatedAt: time.Now().UTC(),
	}, nil
}

// enqueueCaptionsForVoice records the caption request for a freshly
// confirmed voice upload. Durable — no goroutine.
//
// fixes-v2 / Codex P0-2: the v1 version approved safety outright when no
// transcription backend existed ("availability treated as safety"). It no
// longer does. With no backend there is no transcript, therefore no
// verdict, therefore the asset stays gated and is routed to manual review.
func (s *Service) enqueueCaptionsForVoice(ctx context.Context, mediaID, userID uuid.UUID, language string) error {
	if !s.CaptionsBackendConfigured() {
		// No transcript is coming. Evaluate whatever we can (nothing), so
		// the evaluator returns Unavailable and the asset fails closed.
		s.evaluateVoiceSafety(ctx, mediaID, "")
		return nil
	}
	if err := s.pgStore.EnqueueCaptionJob(ctx, mediaID, language, userID); err != nil {
		// P1-4: an enqueue failure previously left the media pending with
		// no durable job — invisible and unretryable. Surface it so
		// ConfirmUpload fails and the client can retry the confirm.
		slog.Error("captions: enqueue failed", "media_id", mediaID, "error", err)
		return fmt.Errorf("could not schedule caption/safety processing: %w", err)
	}
	return nil
}

// evaluateVoiceSafety runs the configured evaluator and persists the
// verdict. This is the ONLY path that may approve a voice asset.
//
//	verdict safe    → approved   (public distribution released)
//	verdict unsafe  → rejected
//	no verdict      → failed     (manual review) — never approved
func (s *Service) evaluateVoiceSafety(ctx context.Context, mediaID uuid.UUID, transcript string) {
	media, err := s.pgStore.GetMedia(ctx, mediaID)
	if err != nil || media == nil || media.FileType != "audio" {
		return
	}
	if media.ModerationStatus != VoiceSafetyPending {
		return // already resolved; do not re-decide
	}

	evaluator := s.audioSafety
	if evaluator == nil {
		evaluator = processing.UnavailableEvaluator{}
	}

	verdict, err := evaluator.EvaluateTranscript(ctx, transcript)
	switch {
	case errors.Is(err, processing.ErrSafetyUnavailable):
		// Fail CLOSED: no verdict is not a safe verdict. Covers provider
		// absence, empty/low-confidence transcript, and unsupported
		// language — each surfaces as ErrSafetyUnavailable.
		s.setVoiceSafety(ctx, mediaID, VoiceSafetyFailed,
			"no safety verdict available (evaluator: "+evaluator.Name()+")")
	case err != nil:
		// Timeout, throttling, malformed response, transport error.
		s.setVoiceSafety(ctx, mediaID, VoiceSafetyFailed, "safety evaluation error: "+err.Error())
	case !verdict.IsSafe:
		// Any evaluator may REJECT, including signal-only ones.
		s.setVoiceSafety(ctx, mediaID, VoiceSafetyRejected, verdict.Reason)
	case !evaluator.CanApprove():
		// LB-2 requirement 6/7: a safe-looking result from an evaluator
		// that is not trusted to approve does NOT release the hold.
		// Trust is declared by the provider contract, never inferred
		// from IsSafe=true.
		s.setVoiceSafety(ctx, mediaID, VoiceSafetyFailed,
			"evaluator "+evaluator.Name()+" is not approval-capable; manual review required")
	default:
		s.setVoiceSafety(ctx, mediaID, VoiceSafetyApproved, "")
	}
}

// setVoiceSafety persists the verdict and announces it so post-service can
// release or reject the held post.
func (s *Service) setVoiceSafety(ctx context.Context, mediaID uuid.UUID, verdict, reason string) {
	if err := s.pgStore.SetMediaModerationStatus(ctx, mediaID, verdict); err != nil {
		slog.Error("voice: safety verdict persist failed", "media_id", mediaID, "error", err)
		return
	}
	slog.Info("voice: safety verdict", "media_id", mediaID, "verdict", verdict, "reason", reason)
	s.publishVoiceSafetyResolved(ctx, mediaID, verdict)
}

// StartCaptionWorker drains caption jobs. Safe in every replica: jobs are
// claimed with FOR UPDATE SKIP LOCKED.
func (s *Service) StartCaptionWorker(ctx context.Context) {
	if !s.CaptionsBackendConfigured() {
		slog.Info("captions: worker not started (no backend configured)")
		return
	}
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				jobs, err := s.pgStore.ClaimCaptionJobs(ctx, 15*time.Minute, 5)
				if err != nil {
					slog.Error("captions: claim failed", "error", err)
					continue
				}
				for i := range jobs {
					job := jobs[i]
					s.runCaptionJob(ctx, job.MediaID, job.Language, job.Attempts, job.ClaimToken)
				}
			}
		}
	}()
}

// captionMaxAttempts bounds retries before a job is parked as failed.
const captionMaxAttempts = 4

// runCaptionJob transcribes, persists, then evaluates safety.
//
// fixes-v2 / Codex P1-4: safety is approved only after ALL of
//
//	(a) the transcript is canonically persisted,
//	(b) the durable job is marked complete under our claim token,
//	(c) a real safety evaluation produced a verdict.
//
// Any of those failing routes to retry or manual review — never approval.
func (s *Service) runCaptionJob(ctx context.Context, mediaID uuid.UUID, language string, attempts int, token *uuid.UUID) {
	// LB-2: take the PROVIDER transcript, not the stored row. The stored
	// row may be owner-authored (CreateSubtitle suppresses the upsert and
	// returns the existing row when edited_by_owner is true), and owner
	// text must never reach the safety evaluator.
	sub, evidence, err := s.GenerateAutoCaptionsWithEvidence(ctx, mediaID, language)
	switch {
	case err != nil && attempts >= captionMaxAttempts:
		_ = s.pgStore.FailCaptionJob(ctx, mediaID, err.Error(), token)
		slog.Error("captions: giving up", "media_id", mediaID, "attempts", attempts, "error", err)
		// No transcript ⇒ no verdict ⇒ manual review.
		s.evaluateVoiceSafety(ctx, mediaID, "")
	case err != nil:
		_ = s.pgStore.ReleaseCaptionJob(ctx, mediaID, err.Error(), token)
		slog.Warn("captions: retrying", "media_id", mediaID, "attempt", attempts, "error", err)
	case sub == nil:
		// Placeholder backend — no real transcript. Honest terminal state.
		_ = s.pgStore.FailCaptionJob(ctx, mediaID, "no transcription backend produced a result", token)
		s.evaluateVoiceSafety(ctx, mediaID, "")
	default:
		// (b) must succeed under our claim token before we may approve.
		if err := s.pgStore.CompleteCaptionJob(ctx, mediaID, token); err != nil {
			slog.Error("captions: completion write failed; not approving safety",
				"media_id", mediaID, "error", err)
			return
		}
		// (c) evaluate the PROVIDER-GENERATED transcript.
		providerText := ""
		if evidence != nil {
			providerText = evidence.Text
		}
		s.evaluateVoiceSafety(ctx, mediaID, providerText)
	}
}

// publishVoiceSafetyResolved notifies post-service so a held voice post
// can leave 'pending'. Best-effort: post-service also re-reads the
// moderation status on demand.
func (s *Service) publishVoiceSafetyResolved(ctx context.Context, mediaID uuid.UUID, verdict string) {
	if s.producer == nil {
		return
	}
	payload, err := json.Marshal(map[string]string{
		"media_id":          mediaID.String(),
		"moderation_status": verdict,
	})
	if err != nil {
		return
	}
	if err := s.producer.PublishRawEvent(ctx, "MediaVoiceSafetyResolved", mediaID.String(), payload); err != nil {
		slog.Warn("voice: safety event publish failed", "media_id", mediaID, "error", err)
	}
}

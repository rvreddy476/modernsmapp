package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Lost-worker cap (long-video audit lv-6, 2026-11-17; migration 033).
//
// The in-process retry cap (retryUntilDurable) counts only runs that RETURN
// an error. A run that is OOM-killed, evicted or crashes returns nothing:
// the offset stays uncommitted, Kafka redelivers the request, and a video
// too big for the pod looped for ever, taking the reels beside it down on
// every cycle. Each run now records a START before any work and a FINISH
// when it returns (any outcome, a clean SIGTERM drain included). A start
// that finds starts-1 > finishes knows that many earlier runs lost their
// worker; at MEDIA_TRANSCODE_MAX_LOST_WORKERS the request is recorded
// failed + manual_review, exactly like a returned terminal error, with the
// reason on a 'pipeline' transcoding_jobs row the status route shows the
// creator.

const (
	maxLostWorkersEnv     = "MEDIA_TRANSCODE_MAX_LOST_WORKERS"
	defaultMaxLostWorkers = 3
	deliveryWriteTimeout  = 10 * time.Second
)

// errLostWorker is the terminal reason once the cap is reached.
var errLostWorker = errors.New("transcode lost its worker too many times (crash, out-of-memory kill or eviction)")

// deliveryCounter is the store's start/finish counter (transcode_delivery.go).
type deliveryCounter interface {
	BeginTranscodeDelivery(ctx context.Context, mediaID uuid.UUID, eventID string) (starts, finishes int, err error)
	FinishTranscodeDelivery(ctx context.Context, mediaID uuid.UUID, eventID string) error
}

// jobRecorder writes transcoding_jobs rows (the status route reads them).
type jobRecorder interface {
	CreateTranscodingJob(ctx context.Context, job *postgres.TranscodingJob) error
	UpdateTranscodingJob(ctx context.Context, jobID uuid.UUID, status string, outputURL *string, outputSizeBytes *int64, errorMessage *string) error
}

// lostRuns is how many earlier runs started and never returned, given the
// counts after this start.
func lostRuns(starts, finishes int) int {
	if n := starts - 1 - finishes; n > 0 {
		return n
	}
	return 0
}

// beginDelivery counts this run's start and returns the lost earlier runs.
// A failure to count is transient: no work starts uncounted.
func beginDelivery(ctx context.Context, store deliveryCounter, mediaID uuid.UUID, eventID string) (int, error) {
	starts, finishes, err := store.BeginTranscodeDelivery(ctx, mediaID, eventID)
	if err != nil {
		if errors.Is(err, postgres.ErrMediaNotFound) {
			return 0, err
		}
		return 0, fmt.Errorf("count transcode start: %w", err)
	}
	lost := lostRuns(starts, finishes)
	kind := startFresh
	if lost > 0 {
		kind = startAfterLostWorker
		log.Printf("Transcode of media %s restarting after %d run(s) lost their worker (start %d, finished %d)",
			mediaID, lost, starts, finishes)
	}
	transcodeStarts.WithLabelValues(classLabel(ctx), kind).Inc()
	return lost, nil
}

// finishDelivery records that this run returned. Written even when the
// work context has ended (a drain past its deadline), and best-effort: a
// lost write makes one clean run look lost, which only brings the cap
// nearer by one.
func finishDelivery(ctx context.Context, store deliveryCounter, mediaID uuid.UUID, eventID string) {
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), deliveryWriteTimeout)
	defer cancel()
	if err := store.FinishTranscodeDelivery(c, mediaID, eventID); err != nil {
		log.Printf("Warning: transcode finish for media %s not recorded: %v", mediaID, err)
	}
}

// lostWorkerFailure records the reason the creator sees and returns the
// terminal error the caller records like any other.
func lostWorkerFailure(ctx context.Context, store jobRecorder, mediaID uuid.UUID, lost int) error {
	cause := fmt.Errorf("%w: %d runs never finished; the video may be too large to process", errLostWorker, lost)
	log.Printf("Transcode of media %s GIVEN UP: %v", mediaID, cause)
	recordJobFailure(ctx, store, mediaID, "pipeline", cause)
	transcodeQuarantined.WithLabelValues(classLabel(ctx), quarantineLostWorker).Inc()
	return permanentTranscode(cause)
}

// recordJobFailure writes a failed transcoding_jobs row named target
// carrying the reason. Best-effort: the terminal failure is recorded either
// way.
func recordJobFailure(ctx context.Context, store jobRecorder, mediaID uuid.UUID, target string, reason error) {
	jobID := uuid.New()
	if err := store.CreateTranscodingJob(ctx, &postgres.TranscodingJob{
		ID: jobID, MediaAssetID: mediaID, TargetQuality: target, Status: "queued",
	}); err != nil {
		log.Printf("Warning: %s failure of media %s not recorded: %v", target, mediaID, err)
		return
	}
	msg := reason.Error()
	if err := store.UpdateTranscodingJob(ctx, jobID, "failed", nil, nil, &msg); err != nil {
		log.Printf("Warning: %s failure of media %s not recorded: %v", target, mediaID, err)
	}
}

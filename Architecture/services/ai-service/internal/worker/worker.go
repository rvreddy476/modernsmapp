package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/atpost/ai-service/internal/provider"
	"github.com/atpost/ai-service/internal/store/postgres"
	"github.com/google/uuid"
)

const (
	defaultPollInterval = 5 * time.Second
	maxRetries          = 3
	batchSize           = 10
)

// Store is the minimal data-access interface the Worker requires.
type Store interface {
	GetPendingJobs(ctx context.Context, limit int) ([]postgres.AIJob, error)
	GetJob(ctx context.Context, id uuid.UUID) (*postgres.AIJob, error)
	UpdateJobStatus(ctx context.Context, id uuid.UUID, status string, result []byte, errMsg string, latencyMs int) error
}

// Worker polls for queued AI jobs and executes them using the configured providers.
type Worker struct {
	store              Store
	textProvider       provider.TextProvider
	moderationProvider provider.ModerationProvider
	concurrency        int
	pollInterval       time.Duration
}

// New returns a Worker with the given dependencies.
// concurrency controls how many jobs are processed in parallel (default 3).
func New(store Store, text provider.TextProvider, mod provider.ModerationProvider, concurrency int) *Worker {
	if concurrency <= 0 {
		concurrency = 3
	}
	return &Worker{
		store:              store,
		textProvider:       text,
		moderationProvider: mod,
		concurrency:        concurrency,
		pollInterval:       defaultPollInterval,
	}
}

// WithPollInterval overrides the default 5-second poll interval. Useful for testing.
func (w *Worker) WithPollInterval(d time.Duration) *Worker {
	w.pollInterval = d
	return w
}

// Run starts the worker's polling loop. It launches concurrency goroutines and
// blocks until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	slog.Info("ai worker starting", "concurrency", w.concurrency)

	sem := make(chan struct{}, w.concurrency)
	var wg sync.WaitGroup

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("ai worker shutting down — waiting for in-flight jobs")
			wg.Wait()
			slog.Info("ai worker stopped")
			return
		case <-ticker.C:
			jobs, err := w.store.GetPendingJobs(ctx, batchSize)
			if err != nil {
				slog.Error("worker: get pending jobs failed", "error", err)
				continue
			}
			for _, job := range jobs {
				job := job // capture
				sem <- struct{}{}
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer func() { <-sem }()
					w.process(ctx, job)
				}()
			}
		}
	}
}

// process executes a single job with retry logic and updates its status.
func (w *Worker) process(ctx context.Context, job postgres.AIJob) {
	// Mark as processing immediately.
	if err := w.store.UpdateJobStatus(ctx, job.ID, "processing", nil, "", 0); err != nil {
		slog.Error("worker: mark job processing failed", "job_id", job.ID, "error", err)
		return
	}

	var (
		result    []byte
		execErr   error
		attempt   int
		latencyMs int
	)

	backoff := []time.Duration{5 * time.Second, 25 * time.Second, 125 * time.Second}

	for attempt = 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			slog.Info("worker: retrying job", "job_id", job.ID, "attempt", attempt+1)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff[attempt-1]):
			}
		}

		start := time.Now()
		result, execErr = w.execute(ctx, job)
		latencyMs = int(time.Since(start).Milliseconds())

		if execErr == nil {
			break
		}
		slog.Warn("worker: job attempt failed", "job_id", job.ID, "attempt", attempt+1, "error", execErr)
	}

	if execErr != nil {
		slog.Error("worker: job failed after retries", "job_id", job.ID, "job_type", job.JobType, "error", execErr)
		if updateErr := w.store.UpdateJobStatus(ctx, job.ID, "failed", nil, execErr.Error(), latencyMs); updateErr != nil {
			slog.Error("worker: mark job failed — update error", "job_id", job.ID, "error", updateErr)
		}
		return
	}

	slog.Info("worker: job completed", "job_id", job.ID, "job_type", job.JobType, "latency_ms", latencyMs)
	if updateErr := w.store.UpdateJobStatus(ctx, job.ID, "completed", result, "", latencyMs); updateErr != nil {
		slog.Error("worker: mark job completed — update error", "job_id", job.ID, "error", updateErr)
	}
}

// execute dispatches a job to the appropriate provider method.
func (w *Worker) execute(ctx context.Context, job postgres.AIJob) ([]byte, error) {
	switch job.JobType {
	case "caption_suggestion", "caption_suggest":
		return w.runCaptionSuggest(ctx, job)
	case "hashtag_suggestion", "hashtag_suggest":
		return w.runHashtagSuggest(ctx, job)
	case "smart_reply":
		return w.runSmartReply(ctx, job)
	case "summary":
		return w.runSummarize(ctx, job)
	case "translation":
		return w.runTranslate(ctx, job)
	case "scam_detection", "scam_check":
		return w.runScamCheck(ctx, job)
	case "engagement_prediction", "engagement_predict":
		return w.runEngagementPredict(ctx, job)
	case "moderation_check":
		return w.runModerationCheck(ctx, job)
	default:
		return nil, fmt.Errorf("unknown job type: %s", job.JobType)
	}
}

// jobInput extracts a string field from a job's result JSONB payload (used as ad-hoc input store).
// For async jobs the caller is expected to embed input in the result field as {"input": "..."}.
func extractInput(job postgres.AIJob, key string) string {
	if len(job.Result) == 0 {
		return ""
	}
	var m map[string]interface{}
	if err := json.Unmarshal(job.Result, &m); err != nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func marshalResult(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

func (w *Worker) runCaptionSuggest(ctx context.Context, job postgres.AIJob) ([]byte, error) {
	content := extractInput(job, "content")
	captions, err := w.textProvider.GenerateCaptions(ctx, content, nil)
	if err != nil {
		return nil, fmt.Errorf("caption suggest: %w", err)
	}
	return marshalResult(map[string]interface{}{"captions": captions})
}

func (w *Worker) runHashtagSuggest(ctx context.Context, job postgres.AIJob) ([]byte, error) {
	content := extractInput(job, "content")
	tags, err := w.textProvider.GenerateHashtags(ctx, content)
	if err != nil {
		return nil, fmt.Errorf("hashtag suggest: %w", err)
	}
	return marshalResult(map[string]interface{}{"hashtags": tags})
}

func (w *Worker) runSmartReply(ctx context.Context, job postgres.AIJob) ([]byte, error) {
	message := extractInput(job, "message")
	convCtx := extractInput(job, "context")
	replies, err := w.textProvider.SmartReply(ctx, message, convCtx)
	if err != nil {
		return nil, fmt.Errorf("smart reply: %w", err)
	}
	return marshalResult(map[string]interface{}{"replies": replies})
}

func (w *Worker) runSummarize(ctx context.Context, job postgres.AIJob) ([]byte, error) {
	text := extractInput(job, "text")
	summary, err := w.textProvider.Summarize(ctx, text, 280)
	if err != nil {
		return nil, fmt.Errorf("summarize: %w", err)
	}
	return marshalResult(map[string]interface{}{"summary": summary})
}

func (w *Worker) runTranslate(ctx context.Context, job postgres.AIJob) ([]byte, error) {
	text := extractInput(job, "text")
	targetLang := extractInput(job, "target_lang")
	if targetLang == "" {
		targetLang = "en"
	}
	translated, err := w.textProvider.Translate(ctx, text, targetLang)
	if err != nil {
		return nil, fmt.Errorf("translate: %w", err)
	}
	return marshalResult(map[string]interface{}{"translated_text": translated, "target_lang": targetLang})
}

func (w *Worker) runScamCheck(ctx context.Context, job postgres.AIJob) ([]byte, error) {
	text := extractInput(job, "text")
	score, reason, err := w.textProvider.ScamCheck(ctx, text)
	if err != nil {
		return nil, fmt.Errorf("scam check: %w", err)
	}
	return marshalResult(map[string]interface{}{"score": score, "reason": reason})
}

func (w *Worker) runEngagementPredict(ctx context.Context, job postgres.AIJob) ([]byte, error) {
	content := extractInput(job, "content")
	score, err := w.textProvider.PredictEngagement(ctx, content)
	if err != nil {
		return nil, fmt.Errorf("engagement predict: %w", err)
	}
	return marshalResult(map[string]interface{}{"engagement_score": score})
}

func (w *Worker) runModerationCheck(ctx context.Context, job postgres.AIJob) ([]byte, error) {
	text := extractInput(job, "text")
	safe, score, categories, err := w.moderationProvider.CheckContent(ctx, text, nil)
	if err != nil {
		return nil, fmt.Errorf("moderation check: %w", err)
	}
	action := "allow"
	if !safe {
		action = "flag"
	}
	return marshalResult(map[string]interface{}{
		"safe":       safe,
		"score":      score,
		"categories": categories,
		"action":     action,
	})
}

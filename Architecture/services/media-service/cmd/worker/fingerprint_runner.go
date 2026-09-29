package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/atpost/media-service/internal/fingerprint"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// The fingerprint runner (Copyright Match phase 1, shadow mode). It lives
// in the transcode worker beside the consumer loop and claims a job only
// when admission allows (plan 13):
//
//   - COPYRIGHT_FINGERPRINT_ENABLED is on (re-read every iteration);
//   - this worker is idle past its grace (busyTracker);
//   - no transcode has been waiting for a worker longer than the queue
//     wait limit, and the circuit breaker is closed;
//   - the backfill flag decides whether priority-10 rows are claimed.
//
// A transcode arriving mid-job pre-empts it: the job goes back to the
// queue with its attempt refunded. Nothing here is visible to a user:
// pairs only leave the database through the pair relay, which has its own
// switch, and only post-service (phase 2) turns them into anything.

var (
	fingerprintJobsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "media_copyright_fingerprint_jobs_total",
		Help: "Fingerprint jobs finished, by result.",
	}, []string{"result"})
	fingerprintDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "media_copyright_fingerprint_duration_seconds",
		Help:    "Wall-clock duration of one fingerprint job (extraction plus lookup).",
		Buckets: []float64{1, 5, 15, 30, 60, 120, 300, 600, 1800},
	})
	fingerprintAdmissionDenied = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "media_copyright_fingerprint_admission_denied_total",
		Help: "Loop iterations that did not claim a job, by reason.",
	}, []string{"reason"})
	fingerprintQueueDepth = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "media_copyright_fingerprint_queue_depth",
		Help: "Queued fingerprint jobs, by priority class.",
	}, []string{"priority"})
	fingerprintQueueOldest = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "media_copyright_fingerprint_queue_oldest_seconds",
		Help: "Age of the oldest queued fingerprint job.",
	})
	fingerprintBreakerOpen = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "media_copyright_fingerprint_breaker_open",
		Help: "1 while the readiness circuit breaker holds fingerprinting off.",
	})
	lookupRows = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "media_copyright_lookup_rows",
		Help:    "Candidate postings scanned per upload.",
		Buckets: prometheus.ExponentialBuckets(100, 4, 9),
	})
	lookupTruncations = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "media_copyright_lookup_truncations_total",
		Help: "Lookups that were truncated, by kind.",
	}, []string{"kind"})
	pairsWritten = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "media_copyright_pairs_written_total",
		Help: "Verified candidates recorded, by class (only full_or_near_full becomes a pair).",
	}, []string{"class"})
)

// fingerprintConfig is re-read from the environment every loop iteration
// so a ConfigMap change takes effect without a redeploy.
type fingerprintConfig struct {
	Enabled         bool
	MatchingEnabled bool
	BackfillEnabled bool
	// IdleGrace: the worker must have been idle this long.
	IdleGrace time.Duration
	// MaxQueuedWait: admission fails when a transcode has waited longer.
	MaxQueuedWait time.Duration
	// BreakerTripAt / BreakerHold: the circuit breaker (plan 13).
	BreakerTripAt time.Duration
	BreakerHold   time.Duration
	// DeadlineBase + DeadlineFactor × duration bounds one job.
	DeadlineBase   time.Duration
	DeadlineFactor float64
	// MaxCandidates verified per upload; the rest are recorded as
	// truncation.
	MaxCandidates int
	// Original fallback caps.
	OriginalMaxBytes int64
	OriginalMaxMs    int
	// Interval between loop iterations.
	Interval time.Duration
	// Threads for ffmpeg.
	Threads int
}

func envFlag(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "on", "yes", "enabled":
		return true
	}
	return false
}

func envDuration(name string, def time.Duration) time.Duration {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= 0 {
			return d
		}
		log.Printf("%s=%q is not a duration; using %s", name, v, def)
	}
	return def
}

func envInt(name string, def int) int {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
		log.Printf("%s=%q is not a non-negative integer; using %d", name, v, def)
	}
	return def
}

func loadFingerprintConfig() fingerprintConfig {
	factor := 0.5
	if v := strings.TrimSpace(os.Getenv("COPYRIGHT_FINGERPRINT_DEADLINE_FACTOR")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			factor = f
		}
	}
	return fingerprintConfig{
		Enabled: envFlag("COPYRIGHT_FINGERPRINT_ENABLED"),
		// COPYRIGHT_INDEX_LOOKUP_ENABLED is the plan's name; either works.
		MatchingEnabled:  envFlag("COPYRIGHT_MATCHING_ENABLED") || envFlag("COPYRIGHT_INDEX_LOOKUP_ENABLED"),
		BackfillEnabled:  envFlag("COPYRIGHT_FINGERPRINT_BACKFILL_ENABLED"),
		IdleGrace:        envDuration("COPYRIGHT_FINGERPRINT_IDLE_GRACE", 60*time.Second),
		MaxQueuedWait:    envDuration("COPYRIGHT_FINGERPRINT_MAX_QUEUE_WAIT", 60*time.Second),
		BreakerTripAt:    envDuration("COPYRIGHT_FINGERPRINT_BREAKER_TRIP", 120*time.Second),
		BreakerHold:      envDuration("COPYRIGHT_FINGERPRINT_BREAKER_HOLD", 15*time.Minute),
		DeadlineBase:     envDuration("COPYRIGHT_FINGERPRINT_DEADLINE_BASE", 60*time.Second),
		DeadlineFactor:   factor,
		MaxCandidates:    envInt("COPYRIGHT_FINGERPRINT_MAX_CANDIDATES", 50),
		OriginalMaxBytes: int64(envInt("COPYRIGHT_FINGERPRINT_ORIGINAL_MAX_MB", 2048)) << 20,
		OriginalMaxMs:    envInt("COPYRIGHT_FINGERPRINT_ORIGINAL_MAX_MINUTES", 180) * 60_000,
		Interval:         envDuration("COPYRIGHT_FINGERPRINT_INTERVAL", 5*time.Second),
		Threads:          envInt("COPYRIGHT_FINGERPRINT_FFMPEG_THREADS", 1),
	}
}

// fingerprintStore is the store surface the runner uses.
type fingerprintStore interface {
	ClaimFingerprintJob(ctx context.Context, maxPriority int, staleAfter time.Duration, maxAttempts int) (*postgres.FingerprintJob, error)
	HeartbeatFingerprintJob(ctx context.Context, key postgres.FingerprintJobKey, token uuid.UUID, progressMs int) (bool, error)
	ReleaseFingerprintJob(ctx context.Context, key postgres.FingerprintJobKey, token uuid.UUID, delay time.Duration, reason string, refund bool) error
	FailFingerprintJob(ctx context.Context, key postgres.FingerprintJobKey, token uuid.UUID, reason string) error
	SkipFingerprintJob(ctx context.Context, key postgres.FingerprintJobKey, token uuid.UUID, reason string) error
	MarkFingerprintJobSuperseded(ctx context.Context, key postgres.FingerprintJobKey, token uuid.UUID) error
	GetMediaGenerationState(ctx context.Context, id uuid.UUID) (*postgres.MediaGenerationState, error)
	GetVariants(ctx context.Context, mediaAssetID uuid.UUID) ([]postgres.MediaVariant, error)
	LoadFingerprint(ctx context.Context, mediaID uuid.UUID, generation int64, algo int) (*postgres.StoredFingerprint, error)
	StoreFingerprint(ctx context.Context, key postgres.FingerprintJobKey, token uuid.UUID, fp postgres.StoredFingerprint, postings []postgres.AnchorPosting) error
	LookupAnchorCandidates(ctx context.Context, p postgres.LookupParams) (*postgres.LookupResult, error)
	LoadCandidate(ctx context.Context, mediaID uuid.UUID, generation int64, algo int) (*postgres.CandidateState, error)
	CompleteFingerprintJob(ctx context.Context, key postgres.FingerprintJobKey, token uuid.UUID, c postgres.FingerprintCompletion) error
	FingerprintQueueStats(ctx context.Context) (postgres.FingerprintQueueStats, error)
	OldestQueuedTranscodeWait(ctx context.Context) (float64, error)
	SweepStalePostings(ctx context.Context, limit int, keepSuperseded time.Duration) (int64, error)
}

// fingerprintRunner holds the loop's dependencies.
type fingerprintRunner struct {
	store      fingerprintStore
	blobs      fingerprintBlobs
	tracker    *busyTracker
	loadConfig func() fingerprintConfig
	scratchDir string

	mu           sync.Mutex
	breakerUntil time.Time
	lastEnabled  *bool
	lastSweep    time.Time
}

// admission decides whether a job may be claimed now. Pure: it takes the
// numbers and returns the reason for a refusal, or "".
func (r *fingerprintRunner) admission(cfg fingerprintConfig, now time.Time, workerBusy bool, queuedWaitS float64) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !cfg.Enabled {
		return "disabled"
	}
	if now.Before(r.breakerUntil) {
		fingerprintBreakerOpen.Set(1)
		return "breaker_open"
	}
	fingerprintBreakerOpen.Set(0)
	if queuedWaitS > cfg.BreakerTripAt.Seconds() && cfg.BreakerTripAt > 0 {
		r.breakerUntil = now.Add(cfg.BreakerHold)
		fingerprintBreakerOpen.Set(1)
		log.Printf("Fingerprint circuit breaker TRIPPED: a transcode has waited %.0fs (> %s); no claims for %s",
			queuedWaitS, cfg.BreakerTripAt, cfg.BreakerHold)
		return "breaker_tripped"
	}
	if workerBusy {
		return "worker_busy"
	}
	if queuedWaitS > cfg.MaxQueuedWait.Seconds() {
		return "transcode_waiting"
	}
	return ""
}

// runFingerprintLoop is the goroutine.
func runFingerprintLoop(ctx context.Context, r *fingerprintRunner) {
	log.Println("Copyright fingerprint runner started (COPYRIGHT_FINGERPRINT_ENABLED decides whether it claims anything)")
	for {
		cfg := r.loadConfig()
		r.logEnabledChange(cfg.Enabled)
		r.exportQueueStats(ctx)
		if cfg.Enabled {
			r.maybeSweep(ctx)
		}
		claimed := r.iterate(ctx, cfg)
		wait := cfg.Interval
		if claimed {
			wait = 0 // there may be more; the admission check runs again first
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func (r *fingerprintRunner) logEnabledChange(enabled bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lastEnabled == nil || *r.lastEnabled != enabled {
		state := "OFF (jobs stay queued)"
		if enabled {
			state = "ON"
		}
		log.Printf("COPYRIGHT_FINGERPRINT_ENABLED is %s", state)
		r.lastEnabled = &enabled
	}
}

func (r *fingerprintRunner) exportQueueStats(ctx context.Context) {
	sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	st, err := r.store.FingerprintQueueStats(sctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("Warning: fingerprint queue stats: %v", err)
		}
		return
	}
	fingerprintQueueDepth.WithLabelValues("upload").Set(float64(st.QueuedNewUploads))
	fingerprintQueueDepth.WithLabelValues("backfill").Set(float64(st.QueuedBackfill))
	fingerprintQueueOldest.Set(st.OldestQueuedAgeS)
}

// maybeSweep runs the stale-postings sweeper every 10 minutes.
func (r *fingerprintRunner) maybeSweep(ctx context.Context) {
	r.mu.Lock()
	due := time.Since(r.lastSweep) >= 10*time.Minute
	if due {
		r.lastSweep = time.Now()
	}
	r.mu.Unlock()
	if !due {
		return
	}
	sctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if n, err := r.store.SweepStalePostings(sctx, 5000, 7*24*time.Hour); err != nil {
		log.Printf("Warning: stale posting sweep: %v", err)
	} else if n > 0 {
		log.Printf("Stale posting sweep removed %d row(s)", n)
	}
}

// iterate performs one admission check and, if allowed, one job. Reports
// whether a job was claimed.
func (r *fingerprintRunner) iterate(ctx context.Context, cfg fingerprintConfig) bool {
	now := time.Now()
	queuedWait := 0.0
	if cfg.Enabled {
		qctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		w, err := r.store.OldestQueuedTranscodeWait(qctx)
		cancel()
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("Warning: transcode queue wait probe failed; not claiming: %v", err)
			}
			fingerprintAdmissionDenied.WithLabelValues("probe_error").Inc()
			return false
		}
		queuedWait = w
	}
	if reason := r.admission(cfg, now, r.tracker.busy(now, cfg.IdleGrace), queuedWait); reason != "" {
		if reason != "disabled" {
			fingerprintAdmissionDenied.WithLabelValues(reason).Inc()
		}
		return false
	}
	maxPriority := postgres.FingerprintPriorityUpload
	if cfg.BackfillEnabled {
		maxPriority = postgres.FingerprintPriorityBackfill
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	job, err := r.store.ClaimFingerprintJob(cctx, maxPriority, postgres.FingerprintLeaseStale, postgres.FingerprintMaxAttempts)
	cancel()
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("Fingerprint claim failed: %v", err)
		}
		return false
	}
	if job == nil {
		return false
	}
	r.runJob(ctx, cfg, job)
	return true
}

// runJob executes one claimed job end to end.
func (r *fingerprintRunner) runJob(ctx context.Context, cfg fingerprintConfig, job *postgres.FingerprintJob) {
	key := job.FingerprintJobKey
	token := *job.ClaimToken
	start := time.Now()
	result := "error"
	defer func() {
		fingerprintJobsTotal.WithLabelValues(result).Inc()
		fingerprintDuration.Observe(time.Since(start).Seconds())
	}()

	st, err := r.store.GetMediaGenerationState(ctx, key.MediaID)
	if err != nil {
		log.Printf("Fingerprint %s gen %d: read state: %v", key.MediaID, key.Generation, err)
		_ = r.store.ReleaseFingerprintJob(ctx, key, token, 5*time.Minute, "read state: "+err.Error(), false)
		return
	}
	if st.FileType != "video" || st.ProcessingStatus != "ready" || st.MediaGeneration != key.Generation ||
		st.ReadyGeneration == nil || *st.ReadyGeneration != key.Generation {
		_ = r.store.MarkFingerprintJobSuperseded(ctx, key, token)
		log.Printf("Fingerprint %s gen %d: superseded before start (status %s, gen %d, ready %v)",
			key.MediaID, key.Generation, st.ProcessingStatus, st.MediaGeneration, st.ReadyGeneration)
		result = "superseded"
		return
	}

	// Deadline: base + factor × duration. Pre-emption: a transcode
	// arriving cancels the job (polled every second).
	deadline := cfg.DeadlineBase + time.Duration(cfg.DeadlineFactor*float64(st.DurationMs))*time.Millisecond
	jobCtx, cancelJob := context.WithTimeout(ctx, deadline)
	defer cancelJob()
	preempted := make(chan struct{})
	var preemptOnce sync.Once
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-jobCtx.Done():
				return
			case <-t.C:
				if r.tracker.busy(time.Now(), 0) {
					preemptOnce.Do(func() { close(preempted) })
					cancelJob()
					return
				}
			}
		}
	}()
	var progressMu sync.Mutex
	progressMs := 0
	setProgress := func(ms int) {
		progressMu.Lock()
		progressMs = ms
		progressMu.Unlock()
	}
	// Heartbeat every 30 s; losing the claim cancels the job.
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-jobCtx.Done():
				return
			case <-t.C:
				progressMu.Lock()
				p := progressMs
				progressMu.Unlock()
				hctx, hcancel := context.WithTimeout(ctx, 10*time.Second)
				owned, err := r.store.HeartbeatFingerprintJob(hctx, key, token, p)
				hcancel()
				if err == nil && !owned {
					log.Printf("Fingerprint %s gen %d: claim lost; stopping", key.MediaID, key.Generation)
					cancelJob()
					return
				}
			}
		}
	}()

	wasPreempted := func() bool {
		select {
		case <-preempted:
			return true
		default:
			return false
		}
	}

	// 1. The fingerprint: reuse a stored one (a reclaim after the first
	//    commit), otherwise extract.
	stored, err := r.store.LoadFingerprint(ctx, key.MediaID, key.Generation, key.Algo)
	if err != nil {
		log.Printf("Fingerprint %s gen %d: load: %v", key.MediaID, key.Generation, err)
		_ = r.store.ReleaseFingerprintJob(ctx, key, token, 5*time.Minute, "load fingerprint: "+err.Error(), false)
		return
	}
	var frames []fingerprint.Frame
	if stored != nil && stored.SupersededAt == nil {
		frames, err = fingerprint.Decode(stored.Frames)
		if err != nil {
			_ = r.store.FailFingerprintJob(ctx, key, token, "stored frames undecodable: "+err.Error())
			result = "failed"
			return
		}
	} else {
		variants, err := r.store.GetVariants(ctx, key.MediaID)
		if err != nil {
			_ = r.store.ReleaseFingerprintJob(ctx, key, token, 5*time.Minute, "list variants: "+err.Error(), false)
			return
		}
		plan, err := selectInput(jobCtx, r.blobs, st, variants, inputCaps{OriginalMaxBytes: cfg.OriginalMaxBytes, OriginalMaxMs: cfg.OriginalMaxMs})
		var skip errSkipInput
		if errors.As(err, &skip) {
			_ = r.store.SkipFingerprintJob(ctx, key, token, skip.reason)
			log.Printf("Fingerprint %s gen %d: skipped (%s)", key.MediaID, key.Generation, skip.reason)
			result = "skipped"
			return
		}
		if err != nil {
			r.releaseOrFail(ctx, key, token, job.Attempts, "select input: "+err.Error())
			return
		}
		ex := fingerprint.Extractor{Threads: cfg.Threads}
		res, etags, err := extractInput(jobCtx, r.blobs, ex, plan, r.scratchDir, setProgress)
		if err != nil {
			switch {
			case wasPreempted():
				_ = r.store.ReleaseFingerprintJob(ctx, key, token, time.Minute, "pre-empted by a transcode", true)
				log.Printf("Fingerprint %s gen %d: pre-empted by a transcode; re-queued", key.MediaID, key.Generation)
				result = "preempted"
			case errors.Is(jobCtx.Err(), context.DeadlineExceeded):
				r.releaseOrFail(ctx, key, token, job.Attempts, fmt.Sprintf("deadline %s exceeded", deadline))
				result = "deadline"
			case ctx.Err() != nil:
				_ = r.store.ReleaseFingerprintJob(ctx, key, token, time.Minute, "worker shutting down", true)
				result = "shutdown"
			default:
				r.releaseOrFail(ctx, key, token, job.Attempts, "extract: "+err.Error())
			}
			return
		}
		if !fingerprint.DurationConsistent(res.MeasuredMs, st.DurationMs) {
			// The objects may be mid-overwrite; retry after the generation
			// re-check (the fence at the next claim).
			r.releaseOrFail(ctx, key, token, job.Attempts, fmt.Sprintf("input_inconsistent: measured %d ms, recorded %d ms (%s %s)",
				res.MeasuredMs, st.DurationMs, plan.Kind, plan.Ref))
			result = "input_inconsistent"
			return
		}
		frames = res.Frames
		weights := fingerprint.Weights(frames)
		informative := fingerprint.InformativeMs(weights)
		spacing := fingerprint.AnchorSpacingS(informative)
		bucket := fingerprint.DurationBucket(informative)
		var postings []postgres.AnchorPosting
		for _, a := range fingerprint.Anchors(frames, spacing) {
			for b := 0; b < fingerprint.Bands; b++ {
				postings = append(postings, postgres.AnchorPosting{BandNo: b, BandValue: fingerprint.Band(a.Hash, b), AnchorMs: a.TMs, Hash: a.Hash})
			}
		}
		err = r.store.StoreFingerprint(ctx, key, token, postgres.StoredFingerprint{
			InputKind: plan.Kind, InputRef: plan.Ref, InputETags: etags,
			DurationMs: res.MeasuredMs, InformativeMs: int(informative), Frames: fingerprint.Encode(frames),
			AnchorSpacingS: spacing, DurBucket: bucket,
		}, postings)
		if r.fenceOutcome(ctx, key, token, err, &result) {
			return
		}
		log.Printf("Fingerprint %s gen %d: %d frames (%d ms informative, %s %s, crop %+v), %d anchors indexed in bucket %d",
			key.MediaID, key.Generation, len(frames), informative, plan.Kind, plan.Ref, res.Crop, len(postings)/fingerprint.Bands, bucket)
	}

	// 2. Lookup, verification, classification.
	completion := postgres.FingerprintCompletion{}
	trunc := map[string]any{}
	if !cfg.MatchingEnabled {
		trunc["lookup"] = "disabled"
	} else {
		if wasPreempted() || jobCtx.Err() != nil {
			_ = r.store.ReleaseFingerprintJob(ctx, key, token, time.Minute, "pre-empted before lookup", true)
			result = "preempted"
			return
		}
		pairs, obs, lookupTrunc, err := r.match(jobCtx, cfg, key, frames)
		if err != nil {
			if wasPreempted() {
				_ = r.store.ReleaseFingerprintJob(ctx, key, token, time.Minute, "pre-empted during lookup", true)
				result = "preempted"
				return
			}
			r.releaseOrFail(ctx, key, token, job.Attempts, "lookup: "+err.Error())
			return
		}
		completion.Pairs, completion.Observations = pairs, obs
		for k, v := range lookupTrunc {
			trunc[k] = v
		}
	}
	if len(trunc) > 0 {
		completion.Truncation, _ = json.Marshal(trunc)
	}
	err = r.store.CompleteFingerprintJob(ctx, key, token, completion)
	if r.fenceOutcome(ctx, key, token, err, &result) {
		return
	}
	result = "done"
	log.Printf("Fingerprint %s gen %d: done (%d pair(s), %d observation(s), %s)",
		key.MediaID, key.Generation, len(completion.Pairs), len(completion.Observations), time.Since(start).Round(time.Millisecond))
}

// fenceOutcome handles the result of a fenced write. Returns true when
// the job is over (superseded, claim lost or error).
func (r *fingerprintRunner) fenceOutcome(ctx context.Context, key postgres.FingerprintJobKey, token uuid.UUID, err error, result *string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, postgres.ErrFingerprintSuperseded):
		_ = r.store.MarkFingerprintJobSuperseded(ctx, key, token)
		log.Printf("Fingerprint %s gen %d: generation fence failed; nothing written", key.MediaID, key.Generation)
		*result = "superseded"
	case errors.Is(err, postgres.ErrFingerprintClaimLost):
		log.Printf("Fingerprint %s gen %d: claim lost at write; nothing written", key.MediaID, key.Generation)
		*result = "claim_lost"
	default:
		log.Printf("Fingerprint %s gen %d: write failed: %v", key.MediaID, key.Generation, err)
		_ = r.store.ReleaseFingerprintJob(ctx, key, token, 5*time.Minute, "write: "+err.Error(), false)
		*result = "error"
	}
	return true
}

// releaseOrFail re-queues with backoff, or fails the job once its attempts
// are used up (the claim already counted this attempt).
func (r *fingerprintRunner) releaseOrFail(ctx context.Context, key postgres.FingerprintJobKey, token uuid.UUID, attempts int, reason string) {
	if attempts >= postgres.FingerprintMaxAttempts {
		if err := r.store.FailFingerprintJob(ctx, key, token, reason); err != nil {
			log.Printf("Fingerprint %s gen %d: could not record failure: %v", key.MediaID, key.Generation, err)
		}
		log.Printf("ALERT fingerprint %s gen %d FAILED after %d attempts: %s", key.MediaID, key.Generation, attempts, reason)
		return
	}
	delay := time.Duration(attempts) * 5 * time.Minute
	if err := r.store.ReleaseFingerprintJob(ctx, key, token, delay, reason, false); err != nil {
		log.Printf("Fingerprint %s gen %d: could not release: %v", key.MediaID, key.Generation, err)
	}
	log.Printf("Fingerprint %s gen %d: attempt %d failed, retry in %s: %s", key.MediaID, key.Generation, attempts, delay, reason)
}

// candidateVotes is one candidate after voting.
type candidateVotes struct {
	MediaID      uuid.UUID
	Generation   int64
	Offsets      []fingerprint.Offset
	MinBandDist  int
	TopVotes     int
	HitsCount    int
	postingsSeen int
}

// match runs lookup → vote → verify → classify for one fingerprint.
func (r *fingerprintRunner) match(ctx context.Context, cfg fingerprintConfig, key postgres.FingerprintJobKey, frames []fingerprint.Frame) (pairs []postgres.PairWrite, obs []postgres.Observation, trunc map[string]any, err error) {
	trunc = map[string]any{}
	weights := fingerprint.Weights(frames)
	informative := fingerprint.InformativeMs(weights)
	query := fingerprint.QueryFrames(frames)
	if len(query) == 0 {
		trunc["query"] = "no_informative_frames"
		return nil, nil, trunc, nil
	}
	hashes := make([]uint64, len(query))
	for i, f := range query {
		hashes[i] = f.Hash
	}
	params := postgres.DefaultLookupParams()
	params.Algo = key.Algo
	params.Buckets = fingerprint.LookupBuckets(informative)
	params.Probes = fingerprint.BuildProbeSet(hashes)
	params.ExcludeMedia = key.MediaID
	res, err := r.store.LookupAnchorCandidates(ctx, params)
	if err != nil {
		return nil, nil, nil, err
	}
	lookupRows.Observe(float64(res.RowsScanned))
	if res.Truncated {
		lookupTruncations.WithLabelValues(res.TruncationKind).Inc()
		trunc[res.TruncationKind] = true
	}
	if res.HotKeysDropped > 0 {
		lookupTruncations.WithLabelValues("hot_key").Inc()
		trunc["hot_keys_dropped"] = res.HotKeysDropped
	}
	if res.StaleDropped > 0 {
		trunc["stale_postings_dropped"] = res.StaleDropped
	}

	// Group postings by candidate, then vote per candidate.
	type ck struct {
		id  uuid.UUID
		gen int64
	}
	grouped := map[ck][]fingerprint.Posting{}
	for _, h := range res.Hits {
		k := ck{h.MediaID, h.Generation}
		grouped[k] = append(grouped[k], fingerprint.Posting{AnchorMs: h.AnchorMs, Hash: h.Hash})
	}
	var cands []candidateVotes
	for k, postings := range grouped {
		hits := fingerprint.Hits(query, postings)
		if len(hits) == 0 {
			continue
		}
		offsets := fingerprint.Vote(hits)
		if len(offsets) == 0 {
			continue
		}
		minBand := fingerprint.BandBits
		for _, h := range hits {
			if h.MinBandDistance < minBand {
				minBand = h.MinBandDistance
			}
		}
		cands = append(cands, candidateVotes{MediaID: k.id, Generation: k.gen, Offsets: offsets,
			MinBandDist: minBand, TopVotes: offsets[0].Votes, HitsCount: len(hits), postingsSeen: len(postings)})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].TopVotes != cands[j].TopVotes {
			return cands[i].TopVotes > cands[j].TopVotes
		}
		return cands[i].MediaID.String() < cands[j].MediaID.String()
	})
	if cfg.MaxCandidates > 0 && len(cands) > cfg.MaxCandidates {
		trunc["candidates_truncated"] = len(cands) - cfg.MaxCandidates
		cands = cands[:cfg.MaxCandidates]
	}
	truncated := res.Truncated

	for _, c := range cands {
		if ctx.Err() != nil {
			return nil, nil, nil, ctx.Err()
		}
		cand, err := r.store.LoadCandidate(ctx, c.MediaID, c.Generation, key.Algo)
		if err != nil {
			return nil, nil, nil, err
		}
		if cand == nil {
			continue // superseded or gone between lookup and now
		}
		refFrames, err := fingerprint.Decode(cand.Fingerprint.Frames)
		if err != nil {
			log.Printf("Fingerprint %s: candidate %s gen %d has undecodable frames; skipped", key.MediaID, c.MediaID, c.Generation)
			continue
		}
		align := fingerprint.Align(frames, refFrames, c.Offsets)
		scores := fingerprint.Score(frames, refFrames, align, truncated)
		class := fingerprint.Classify(scores)
		pairsWritten.WithLabelValues(string(class)).Inc()
		ps := postgres.PairScores{
			Class: string(class), RefCoverage: scores.RefCoverage, CopyCoverage: scores.CopyCoverage,
			MatchedS: scores.MatchedS, MatchedInformativeS: scores.MatchedInformativeS,
			MedianHamming: scores.MedianHamming, P90Hamming: scores.P90Hamming, Diversity: scores.Diversity,
		}
		other := postgres.PairSide{MediaID: cand.MediaID, Generation: cand.Generation,
			UploadConfirmed: cand.UploadConfirmed, UploadTimeSource: cand.UploadTimeSource}
		obs = append(obs, postgres.Observation{Other: other, MinBandDistance: c.MinBandDist, Truncated: truncated, PairScores: ps})
		if class == fingerprint.ClassFullOrNearFull {
			pairs = append(pairs, postgres.PairWrite{Copy: r.selfSide(ctx, key), Ref: other, Algo: key.Algo, PairScores: ps})
		}
	}
	return pairs, obs, trunc, nil
}

// selfSide reads the job asset's upload-time identity for the pair row.
func (r *fingerprintRunner) selfSide(ctx context.Context, key postgres.FingerprintJobKey) postgres.PairSide {
	side := postgres.PairSide{MediaID: key.MediaID, Generation: key.Generation}
	if st, err := r.store.GetMediaGenerationState(ctx, key.MediaID); err == nil {
		side.UploadConfirmed, side.UploadTimeSource = st.UploadConfirmed, st.UploadTimeSource
	}
	return side
}

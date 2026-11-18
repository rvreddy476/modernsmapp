package main

import (
	"context"
	"log"
	"os"
	"time"
)

// SIGTERM drain (long-video audit lv-5 / reels-56, 2026-11-17).
//
// On SIGTERM the worker used to cancel its one root context, and every
// ffmpeg ran under exec.CommandContext of it, so a rollout or node drain
// killed a multi-hour encode at once and the pod's grace period bought
// nothing. There are now two contexts:
//
//   - STOP, cancelled on the first SIGTERM/SIGINT: the consumers fetch no
//     new message, the retry loop starts no new attempt, and the rung
//     pipeline starts no new rendition (rungs.go, errWorkerDraining).
//   - WORK, cancelled only at the drain deadline (or a second signal): every
//     ffmpeg, upload and database write of the job in hand runs under it, so
//     the rendition being encoded finishes, uploads and records its marker.
//
// After the drain the run returns, its delivery is recorded FINISHED (not a
// lost worker, transcode_delivery.go) and the offset stays uncommitted, so
// the next owner of the partition resumes from the first missing rendition.
//
// THE WINDOW FOLLOWS THE POD (REG-2, 2026-11-18). The window used to be the
// class's ASSUMED grace (3600 s for a worker consuming the long class) minus
// 60 s, but staging, Azure and the local compose give the pod 300 s or less,
// so the kubelet hard-killed every drained long run. The chart now passes
// the pod's own terminationGracePeriodSeconds in
// MEDIA_WORKER_TERMINATION_GRACE_SECONDS, and the window is that grace minus
// a release margin (a fifth of it, at least 5 s and at most 60 s). With the
// variable absent the window is a short 20 s, which fits Kubernetes' default
// 30 s grace. MEDIA_WORKER_DRAIN_SECONDS may shorten the window, never
// lengthen it past the grace.
//
// A drain is recorded durably (DrainTranscodeDelivery) BEFORE the window is
// waited out, so a run killed at the end of the grace period is a drained
// run, never a lost worker (delivery.go).

const (
	drainSecondsEnv = "MEDIA_WORKER_DRAIN_SECONDS"
	// graceSecondsEnv is the pod's terminationGracePeriodSeconds, set by the
	// chart from the same value.
	graceSecondsEnv = "MEDIA_WORKER_TERMINATION_GRACE_SECONDS"
)

const (
	// unknownGraceDrain is the window when the grace is not given.
	unknownGraceDrain = 20 * time.Second
	minReleaseRoom    = 5 * time.Second
	maxReleaseRoom    = 60 * time.Second
)

// drainWindow is how long the job in hand may keep running after SIGTERM.
func drainWindow(getenv func(string) string) time.Duration {
	limit := unknownGraceDrain
	if secs := envIntFrom(getenv, graceSecondsEnv, -1); secs >= 0 {
		grace := time.Duration(secs) * time.Second
		room := grace / 5
		if room < minReleaseRoom {
			room = minReleaseRoom
		}
		if room > maxReleaseRoom {
			room = maxReleaseRoom
		}
		if limit = grace - room; limit < 0 {
			limit = 0
		}
	}
	if v := getenv(drainSecondsEnv); v != "" {
		if w := time.Duration(envIntFrom(getenv, drainSecondsEnv, 0)) * time.Second; w < limit {
			return w
		}
		log.Printf("%s=%q is longer than the pod's grace allows; using %s", drainSecondsEnv, v, limit) //nolint:gosec // G706: operator env value, %q-quoted so it cannot forge log lines
	}
	return limit
}

// ── the drain signal on the context (delivery.go records the drain) ──

type drainKey struct{}

// withDrainSignal carries the stop context's Done channel to the job, which
// records its drain the moment the worker stops taking work.
func withDrainSignal(ctx context.Context, draining <-chan struct{}) context.Context {
	return context.WithValue(ctx, drainKey{}, draining)
}

// drainSignal is the channel closed when the worker starts draining; nil
// (never) when the context carries none.
func drainSignal(ctx context.Context) <-chan struct{} {
	if ch, ok := ctx.Value(drainKey{}).(<-chan struct{}); ok {
		return ch
	}
	return nil
}

// drainContexts returns the stop and work contexts (see the file comment),
// driven by signals. cancel releases both.
func drainContexts(signals <-chan os.Signal, window time.Duration) (stopCtx, workCtx context.Context, cancel func()) {
	stopCtx, stop := context.WithCancel(context.Background())
	workCtx, cancelWork := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		select {
		case <-signals:
		case <-done:
			return
		}
		log.Printf("Shutting down worker: no new jobs; the rendition in hand may run %s more", window)
		stop()
		timer := time.NewTimer(window)
		defer timer.Stop()
		select {
		case <-timer.C:
			log.Println("Drain deadline reached; stopping the job in hand")
		case <-signals:
			log.Println("Second signal; stopping the job in hand now")
		case <-done:
		}
		cancelWork()
	}()
	return stopCtx, workCtx, func() {
		select {
		case <-done:
		default:
			close(done)
		}
		stop()
		cancelWork()
	}
}

// stoppingFunc reports whether the stop context has ended.
func stoppingFunc(stopCtx context.Context) func() bool {
	return func() bool { return stopCtx.Err() != nil }
}

// ── class on the context (for labelled metrics deep in the job) ──────

type classKey struct{}

func withTranscodeClass(ctx context.Context, c transcodeClass) context.Context {
	return context.WithValue(ctx, classKey{}, c)
}

func classLabel(ctx context.Context) string {
	if c, ok := ctx.Value(classKey{}).(transcodeClass); ok {
		return string(c)
	}
	return "unknown"
}

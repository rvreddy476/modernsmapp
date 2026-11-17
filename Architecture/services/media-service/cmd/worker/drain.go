package main

import (
	"context"
	"log"
	"os"
	"slices"
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
// The deadline must sit inside terminationGracePeriodSeconds, with room for
// the release writes. Default: the class's grace (600 s short, 3600 s long)
// minus 60 s; MEDIA_WORKER_DRAIN_SECONDS overrides it.

const drainSecondsEnv = "MEDIA_WORKER_DRAIN_SECONDS"

const (
	shortGraceDefault = 600 * time.Second
	longGraceDefault  = 3600 * time.Second
	drainReleaseRoom  = 60 * time.Second
)

// drainWindow is how long the job in hand may keep running after SIGTERM.
func drainWindow(getenv func(string) string, classes []transcodeClass) time.Duration {
	if v := getenv(drainSecondsEnv); v != "" {
		return time.Duration(envIntFrom(getenv, drainSecondsEnv, 0)) * time.Second
	}
	grace := shortGraceDefault
	if slices.Contains(classes, classLong) {
		grace = longGraceDefault
	}
	return grace - drainReleaseRoom
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

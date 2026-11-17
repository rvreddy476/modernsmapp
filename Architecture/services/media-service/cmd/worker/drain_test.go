package main

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
)

// lv-5: SIGTERM stops new work at once but leaves the job in hand its
// drain window; ffmpeg is killed only at the deadline.
func TestSIGTERMStopsNewWorkButNotTheJobInHand(t *testing.T) {
	sig := make(chan os.Signal, 2)
	stopCtx, workCtx, cancel := drainContexts(sig, 300*time.Millisecond)
	defer cancel()
	sig <- syscall.SIGTERM
	select {
	case <-stopCtx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("stop context not cancelled by SIGTERM")
	}
	if workCtx.Err() != nil {
		t.Fatal("work context cancelled at SIGTERM: ffmpeg would be killed at once")
	}
	select {
	case <-workCtx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("work context outlived the drain window")
	}
}

func TestSecondSignalEndsTheDrainAtOnce(t *testing.T) {
	sig := make(chan os.Signal, 2)
	_, workCtx, cancel := drainContexts(sig, time.Hour)
	defer cancel()
	sig <- syscall.SIGTERM
	sig <- syscall.SIGINT
	select {
	case <-workCtx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("second signal did not end the drain")
	}
}

func TestDrainWindowFitsTheClassGracePeriod(t *testing.T) {
	none := func(string) string { return "" }
	if got := drainWindow(none, []transcodeClass{classShort}); got != 540*time.Second {
		t.Fatalf("short drain = %s, want 540s (600s grace - 60s)", got)
	}
	if got := drainWindow(none, []transcodeClass{classLong}); got != 3540*time.Second {
		t.Fatalf("long drain = %s, want 3540s (3600s grace - 60s)", got)
	}
	if got := drainWindow(none, []transcodeClass{classShort, classLong}); got != 3540*time.Second {
		t.Fatalf("mixed drain = %s", got)
	}
	set := func(k string) string {
		if k == drainSecondsEnv {
			return "120"
		}
		return ""
	}
	if got := drainWindow(set, []transcodeClass{classLong}); got != 120*time.Second {
		t.Fatalf("override = %s", got)
	}
}

// A message fetched as the drain began is left for the next owner: no new
// attempt starts after SIGTERM.
func TestNoAttemptStartsAfterStop(t *testing.T) {
	stopCtx, stop := context.WithCancel(context.Background())
	stop()
	// A message that WOULD be handled durably at once if it ran (not a
	// transcode request: processMessage returns nil), so only the stop check
	// can keep it uncommitted.
	m := kafka.Message{Value: []byte(`{"event_type":"something.else","event_id":"e"}`)}
	if !handleUntilDurable(context.Background(), context.Background(), classLong, m, nil, nil, nil) {
		t.Fatal("control: the message should be durable when the worker is not stopping")
	}
	if handleUntilDurable(stopCtx, context.Background(), classLong, m, nil, nil, nil) {
		t.Fatal("a message fetched after SIGTERM was run and reported durable")
	}
}

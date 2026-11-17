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

// REG-2: the drain window follows the pod's real grace period, never a
// grace assumed from the class.
func TestDrainWindowFollowsThePodsGracePeriod(t *testing.T) {
	env := func(kv ...string) func(string) string {
		return func(k string) string {
			for i := 0; i+1 < len(kv); i += 2 {
				if kv[i] == k {
					return kv[i+1]
				}
			}
			return ""
		}
	}
	for _, c := range []struct {
		name string
		get  func(string) string
		want time.Duration
	}{
		{"absent: short default", env(), 20 * time.Second},
		{"chart default 300", env(graceSecondsEnv, "300"), 240 * time.Second},
		{"prod short 600", env(graceSecondsEnv, "600"), 540 * time.Second},
		{"prod long 3600", env(graceSecondsEnv, "3600"), 3540 * time.Second},
		{"k8s default 30", env(graceSecondsEnv, "30"), 24 * time.Second},
		{"compose 10", env(graceSecondsEnv, "10"), 5 * time.Second},
		{"tiny grace", env(graceSecondsEnv, "3"), 0},
		{"override shortens", env(graceSecondsEnv, "3600", drainSecondsEnv, "120"), 120 * time.Second},
		{"override cannot outlast the grace", env(graceSecondsEnv, "300", drainSecondsEnv, "3540"), 240 * time.Second},
		{"override cannot outlast the unknown default", env(drainSecondsEnv, "3540"), 20 * time.Second},
		{"garbage grace is unknown", env(graceSecondsEnv, "soon"), 20 * time.Second},
	} {
		if got := drainWindow(c.get); got != c.want {
			t.Errorf("%s: drain = %s, want %s", c.name, got, c.want)
		}
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

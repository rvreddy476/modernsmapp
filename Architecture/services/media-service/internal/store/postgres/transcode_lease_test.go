package postgres

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The stall sweeper's selection rule (migration 021). Each case names the
// guard it pins; the integration suite proves the SQL agrees.

var leaseNow = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

func ago(d time.Duration) *time.Time { t := leaseNow.Add(-d); return &t }

func candidate(mut func(*StalledTranscodeCandidate)) StalledTranscodeCandidate {
	c := StalledTranscodeCandidate{
		MediaID:          uuid.New(),
		FileType:         "video",
		ProcessingStatus: "processing",
		UpdatedAt:        leaseNow.Add(-5 * time.Hour),
	}
	if mut != nil {
		mut(&c)
	}
	return c
}

func TestStallRuleFreshHeartbeatIsSkipped(t *testing.T) {
	// A three-hour job whose updated_at is hours old but whose heartbeat is a
	// minute old is alive. updated_at must play no part.
	c := candidate(func(c *StalledTranscodeCandidate) { c.HeartbeatAt = ago(time.Minute) })
	for _, busy := range []bool{false, true} {
		if v := ClassifyStalledTranscode(c, DefaultStallPolicy(), leaseNow, busy); v.Action != StallSkip {
			t.Fatalf("busy=%v: fresh heartbeat got %s (%s), want skip", busy, v.Action, v.Reason)
		}
	}
	// Just inside the threshold is still alive.
	c.HeartbeatAt = ago(10*time.Minute - time.Second)
	if v := ClassifyStalledTranscode(c, DefaultStallPolicy(), leaseNow, false); v.Action != StallSkip {
		t.Fatalf("heartbeat 9m59s old got %s, want skip", v.Action)
	}
}

func TestStallRuleStaleHeartbeatIsRequeued(t *testing.T) {
	c := candidate(func(c *StalledTranscodeCandidate) { c.HeartbeatAt = ago(11 * time.Minute) })
	// A dead job is dead whether or not other work is running.
	for _, busy := range []bool{false, true} {
		v := ClassifyStalledTranscode(c, DefaultStallPolicy(), leaseNow, busy)
		if v.Action != StallRequeue {
			t.Fatalf("busy=%v: stale heartbeat got %s (%s), want requeue", busy, v.Action, v.Reason)
		}
		if !strings.Contains(v.Reason, "no transcode heartbeat for 11m0s") || !strings.Contains(v.Reason, "1 of 3") {
			t.Fatalf("reason %q does not say what happened", v.Reason)
		}
	}
	// The job's updated_at being recent changes nothing: the heartbeat rules.
	c.UpdatedAt = leaseNow.Add(-time.Minute)
	if v := ClassifyStalledTranscode(c, DefaultStallPolicy(), leaseNow, false); v.Action != StallRequeue {
		t.Fatalf("stale heartbeat with recent updated_at got %s, want requeue", v.Action)
	}
}

func TestStallRuleNullHeartbeatOldUpdateIsRequeuedWhenIdle(t *testing.T) {
	c := candidate(func(c *StalledTranscodeCandidate) { c.UpdatedAt = leaseNow.Add(-31 * time.Minute) })
	v := ClassifyStalledTranscode(c, DefaultStallPolicy(), leaseNow, false)
	if v.Action != StallRequeue {
		t.Fatalf("orphan with idle pipeline got %s (%s), want requeue", v.Action, v.Reason)
	}
	if !strings.Contains(v.Reason, "never started") {
		t.Fatalf("reason %q", v.Reason)
	}
}

func TestStallRuleNullHeartbeatQueuedBehindRunningWorkIsSkipped(t *testing.T) {
	// The dev state of 2026-09-29: queued for hours behind one long job.
	c := candidate(func(c *StalledTranscodeCandidate) { c.UpdatedAt = leaseNow.Add(-36 * time.Hour) })
	if v := ClassifyStalledTranscode(c, DefaultStallPolicy(), leaseNow, true); v.Action != StallSkip {
		t.Fatalf("queued behind running work got %s (%s), want skip", v.Action, v.Reason)
	}
}

func TestStallRuleNullHeartbeatRecentlyQueuedIsSkipped(t *testing.T) {
	c := candidate(func(c *StalledTranscodeCandidate) { c.UpdatedAt = leaseNow.Add(-29 * time.Minute) })
	if v := ClassifyStalledTranscode(c, DefaultStallPolicy(), leaseNow, false); v.Action != StallSkip {
		t.Fatalf("recently queued got %s (%s), want skip", v.Action, v.Reason)
	}
}

func TestStallRuleInFlightOutboxIsSkipped(t *testing.T) {
	for name, c := range map[string]StalledTranscodeCandidate{
		"stale heartbeat": candidate(func(c *StalledTranscodeCandidate) {
			c.HeartbeatAt = ago(time.Hour)
			c.InFlight = true
		}),
		"orphan": candidate(func(c *StalledTranscodeCandidate) { c.InFlight = true }),
		"at the cap": candidate(func(c *StalledTranscodeCandidate) {
			c.HeartbeatAt = ago(time.Hour)
			c.Attempts = 5
			c.InFlight = true
		}),
	} {
		if v := ClassifyStalledTranscode(c, DefaultStallPolicy(), leaseNow, false); v.Action != StallSkip {
			t.Fatalf("%s: in-flight got %s (%s), want skip", name, v.Action, v.Reason)
		}
	}
}

func TestStallRuleAttemptsCapGivesUp(t *testing.T) {
	p := DefaultStallPolicy()
	below := candidate(func(c *StalledTranscodeCandidate) {
		c.HeartbeatAt = ago(time.Hour)
		c.Attempts = p.MaxAttempts - 1
	})
	if v := ClassifyStalledTranscode(below, p, leaseNow, false); v.Action != StallRequeue {
		t.Fatalf("attempts %d got %s, want requeue", below.Attempts, v.Action)
	}
	at := below
	at.Attempts = p.MaxAttempts
	v := ClassifyStalledTranscode(at, p, leaseNow, false)
	if v.Action != StallGiveUp {
		t.Fatalf("attempts %d got %s (%s), want give_up", at.Attempts, v.Action, v.Reason)
	}
	if !strings.Contains(v.Reason, "gave up after 3 automatic re-queues") {
		t.Fatalf("give-up reason %q is not clear", v.Reason)
	}
	// A never-started orphan at the cap gives up too — but only when idle.
	orphan := candidate(func(c *StalledTranscodeCandidate) { c.Attempts = p.MaxAttempts })
	if v := ClassifyStalledTranscode(orphan, p, leaseNow, false); v.Action != StallGiveUp {
		t.Fatalf("orphan at cap got %s, want give_up", v.Action)
	}
	if v := ClassifyStalledTranscode(orphan, p, leaseNow, true); v.Action != StallSkip {
		t.Fatalf("orphan at cap with busy pipeline got %s, want skip", v.Action)
	}
}

func TestStallRuleOnlyProcessingVideos(t *testing.T) {
	for name, c := range map[string]StalledTranscodeCandidate{
		"ready":   candidate(func(c *StalledTranscodeCandidate) { c.ProcessingStatus = "ready"; c.HeartbeatAt = ago(time.Hour) }),
		"failed":  candidate(func(c *StalledTranscodeCandidate) { c.ProcessingStatus = "failed" }),
		"image":   candidate(func(c *StalledTranscodeCandidate) { c.FileType = "image" }),
		"audio":   candidate(func(c *StalledTranscodeCandidate) { c.FileType = "audio"; c.HeartbeatAt = ago(time.Hour) }),
		"pending": candidate(func(c *StalledTranscodeCandidate) { c.ProcessingStatus = "pending_upload" }),
	} {
		if v := ClassifyStalledTranscode(c, DefaultStallPolicy(), leaseNow, false); v.Action != StallSkip {
			t.Fatalf("%s got %s, want skip", name, v.Action)
		}
	}
}

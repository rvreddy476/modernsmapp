package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atpost/media-service/internal/store/blob"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

const gib = int64(1) << 30

// lv-13: statfs on the emptyDir reports the node's 100 GiB disk; the
// volume's own 50 GiB limit, minus what other jobs already wrote, is what
// the job may use. A 16 GiB original (48 GiB with headroom) with 10 GiB
// already on the volume is refused before the download.
func TestScratchCheckUsesTheVolumesOwnLimit(t *testing.T) {
	node := scratchSpace{free: 90 * gib, total: 100 * gib}
	space, known := boundedScratchSpace(node, true, 50*gib, 10*gib)
	if !known || space.total != 50*gib || space.free != 40*gib {
		t.Fatalf("space = %+v known=%v, want 40 of 50 GiB", space, known)
	}
	subject := &transcodeSubject{ID: uuid.New()}
	err := checkOriginalBeforeDownload(blob.ObjectInfo{Size: 16 * gib}, subject, 0, space, known)
	if err == nil || !strings.Contains(err.Error(), "free") {
		t.Fatalf("err = %v, want a refusal for free space", err)
	}
	// Unbounded (no limit configured): the node figure, as before.
	if s, _ := boundedScratchSpace(node, true, 0, 10*gib); s != node {
		t.Fatalf("no limit changed the space: %+v", s)
	}
	// The node can be the smaller of the two.
	if s, _ := boundedScratchSpace(scratchSpace{free: 5 * gib, total: 100 * gib}, true, 50*gib, 0); s.free != 5*gib {
		t.Fatalf("free = %d, want the node's 5 GiB", s.free)
	}
	// statfs unavailable: the limit alone is known.
	if s, k := boundedScratchSpace(scratchSpace{}, false, 50*gib, 60*gib); !k || s.free != 0 {
		t.Fatalf("over-full volume = %+v %v, want 0 free", s, k)
	}
}

func TestDirBytesCountsEveryJobOnTheVolume(t *testing.T) {
	d := t.TempDir()
	_ = os.MkdirAll(filepath.Join(d, "transcode-a"), 0o700)
	_ = os.WriteFile(filepath.Join(d, "transcode-a", "original"), make([]byte, 300), 0o600)
	_ = os.WriteFile(filepath.Join(d, "x"), make([]byte, 50), 0o600)
	if got := dirBytes(d); got != 350 {
		t.Fatalf("dirBytes = %d, want 350", got)
	}
}

func TestScratchLimitIsReadFromTheEnvironment(t *testing.T) {
	cfg := loadWorkerConfig(func(k string) string {
		if k == scratchLimitEnv {
			return "53687091200"
		}
		return ""
	})
	if cfg.scratchLimitBytes != 50*gib {
		t.Fatalf("scratchLimitBytes = %d", cfg.scratchLimitBytes)
	}
	if cfg.maxLostWorkers != defaultMaxLostWorkers {
		t.Fatalf("maxLostWorkers = %d", cfg.maxLostWorkers)
	}
}

// Alert rules use increase(): every alerted series exists at 0 from start.
func TestAlertedCountersArePreCreated(t *testing.T) {
	precreateWorkerMetrics()
	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]map[string]string{
		"media_transcode_quarantined_total": {
			{"class": "short", "reason": "permanent"}, {"class": "long", "reason": "lost_worker"},
			{"class": "long", "reason": "attempts_exhausted"},
		},
		"media_transcode_stall_reclaims_total": {{"action": "give_up"}},
		"media_transcode_routed_total":         {{"outcome": "forward_failed"}},
		"media_transcode_starts_total":         {{"class": "long", "kind": "after_lost_worker"}},
	}
	for name, sets := range want {
		var fam *dto.MetricFamily
		for _, mf := range mfs {
			if mf.GetName() == name {
				fam = mf
			}
		}
		if fam == nil {
			t.Fatalf("%s not registered", name)
		}
		for _, labels := range sets {
			if !hasSeries(fam, labels) {
				t.Fatalf("%s%v not pre-created", name, labels)
			}
		}
	}
}

func hasSeries(fam *dto.MetricFamily, labels map[string]string) bool {
	for _, m := range fam.GetMetric() {
		match := 0
		for _, lp := range m.GetLabel() {
			if v, ok := labels[lp.GetName()]; ok && v == lp.GetValue() {
				match++
			}
		}
		if match == len(labels) {
			return true
		}
	}
	return false
}

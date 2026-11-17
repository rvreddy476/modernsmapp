//go:build integration

package integration

// Long-video journey 6 (lv-5): the transcode worker is killed in the middle
// of a long-class transcode and started again. The video still becomes
// ready, the renditions finished before the kill are reused rather than
// encoded again (media-service cmd/worker/rungs.go durable markers), and a
// second account plays it.
//
// Process control is the tier's own: Architecture/docker/run-local.sh
// `stop --signal KILL media-worker` (a crash: no drain, no finish record),
// then `run-local.sh media-worker`, appending to the same service log. The
// e2e workflow names the script in ATPOST_E2E_RUN_LOCAL.
//
// NOT parallel. Go runs every sequential top-level test before any parallel
// one, so this flow has the worker to itself: no other transcode is cut by
// the kill, and the worker's rung counter moves only for this video.

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// workerMetricsURL is the media worker's Prometheus endpoint (run-local.sh
// METRICS_PORT 9191).
func workerMetricsURL() string {
	return envOr("ATPOST_E2E_WORKER_METRICS_URL", "http://localhost:9191/metrics")
}

// workerRungsEncoded reads media_transcode_rungs_total{outcome="encoded"}
// (cmd/worker/rungs.go): units written with their durable marker since the
// worker started. ok is false when the worker does not answer.
func workerRungsEncoded(ctx context.Context) (float64, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, workerMetricsURL(), nil)
	if err != nil {
		return 0, false
	}
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, false
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "media_transcode_rungs_total{") || !strings.Contains(line, `outcome="encoded"`) {
			continue
		}
		fields := strings.Fields(line)
		if v, err := strconv.ParseFloat(fields[len(fields)-1], 64); err == nil {
			return v, true
		}
	}
	return 0, true // listening, no unit encoded yet (the series is created on first use)
}

// serviceLogCount counts the service-log lines holding every one of parts.
func serviceLogCount(parts ...string) int {
	b, err := os.ReadFile(serviceLogPath())
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(string(b), "\n") {
		hit := true
		for _, s := range parts {
			if !strings.Contains(line, s) {
				hit = false
				break
			}
		}
		if hit {
			n++
		}
	}
	return n
}

// stopWorker kills the media worker as a crash would (run-local.sh stop).
func stopWorker(t *testing.T, ctx context.Context, runLocal string) {
	t.Helper()
	c, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(c, "bash", runLocal, "stop", "--signal", "KILL", "media-worker").CombinedOutput()
	t.Logf("run-local.sh stop --signal KILL media-worker: %s", strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("stopping the media worker failed: %v", err)
	}
}

// startWorker starts the media worker again in the background, appending to
// the service log, with the environment the tier started it with: the test
// step's own, plus the two values only the "Start services" step sets.
// The process outlives the test: the flows after this one need it.
func startWorker(t *testing.T, runLocal string) {
	t.Helper()
	f, err := os.OpenFile(serviceLogPath(), os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatalf("open the service log to restart the worker: %v", err)
	}
	defer f.Close()
	cmd := exec.Command("bash", runLocal, "media-worker")
	cmd.Stdout, cmd.Stderr = f, f
	env := os.Environ()
	if os.Getenv("ENV") == "" {
		env = append(env, "ENV=local")
	}
	if v := os.Getenv("ATPOST_E2E_MAX_VIDEO_SECONDS"); v != "" && os.Getenv("MEDIA_MAX_VIDEO_DURATION_SEC") == "" {
		env = append(env, "MEDIA_MAX_VIDEO_DURATION_SEC="+v)
	}
	cmd.Env = env
	if err := cmd.Start(); err != nil {
		t.Fatalf("restart the media worker: %v", err)
	}
	_ = cmd.Process.Release()
}

// TestE2E_Video_LongVideo_WorkerKilledMidTranscode (lv-5, journey 6):
//
//	A uploads a long-class clip (605 s, 1280x720, enough frames that each
//	  rendition takes seconds) and creates a public long video
//	once the worker has recorded the thumbnail and the 360p unit (its rung
//	  counter moved by two) and the asset is still processing: SIGKILL
//	the worker is started again; Kafka redelivers the uncommitted request
//	the worker logs the lost run and "Rendition 360p of media <id> reused
//	  from an earlier run"
//	the asset becomes ready+passed, the post playable, and B plays every
//	  rung (360p, 720p)
func TestE2E_Video_LongVideo_WorkerKilledMidTranscode(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.APIGateway)
	aID, bID := E2EUsers(t)
	// Deliberately not t.Parallel(): see the file comment.

	runLocal := os.Getenv("ATPOST_E2E_RUN_LOCAL")
	if runLocal == "" {
		missingStack(t, "set ATPOST_E2E_RUN_LOCAL to Architecture/docker/run-local.sh: the flow stops and starts the media worker with it")
		return
	}
	if serviceLogPath() == "" {
		missingStack(t, "set ATPOST_E2E_SERVICE_LOG to the stack's service log: the flow reads the worker's lines")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	base, up := workerRungsEncoded(ctx)
	if !up {
		missingStack(t, "the media worker's metrics (%s) do not answer", workerMetricsURL())
		return
	}

	a := NewHTTPClient(urls.APIGateway, aID)
	b := NewHTTPClient(urls.APIGateway, bID)
	ensureChannel(t, ctx, a)

	// 15 frames a second: ~9,000 frames a rung, so the 480p and 720p units
	// take long enough to be cut. A different picture from the other long
	// flows' clips (negated, hue-shifted), so it is no duplicate of theirs.
	fps := envInt("ATPOST_E2E_KILL_FIXTURE_FPS", 15)
	d := strconv.Itoa(longClassSeconds)
	clip := ffmpegFixture(t, "long-kill.mp4",
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc2=size=1280x720:rate=%d:duration=%s", fps, d),
		"-f", "lavfi", "-i", "sine=frequency=550:sample_rate=8000:duration="+d,
		"-vf", "negate,hue=h=120",
		"-c:v", "libx264", "-preset", "ultrafast", "-crf", "38", "-g", "300", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-ac", "1", "-b:a", "8k", "-shortest", "-movflags", "+faststart")
	t.Logf("kill fixture: %d bytes, %d s at %d fps, 1280x720", len(clip), longClassSeconds, fps)

	startedBefore := serviceLogCount("[media-worker]", "Media transcode worker started")
	mediaID := resumableUploadAll(t, ctx, a, clip)
	post := createVideoPost(t, ctx, a, "long_video", "public", "E2E worker kill "+uuid.NewString()[:8], mediaID)

	// Once killed, the worker must come back whatever fails below: every
	// flow after this one needs it.
	killed, restarted := false, false
	t.Cleanup(func() {
		if killed && !restarted {
			startWorker(t, runLocal)
		}
	})

	// ── wait for two finished units (thumbnail, 360p), then kill ──
	deadline := time.Now().Add(8 * time.Minute)
	last := base
	for {
		n, ok := workerRungsEncoded(ctx)
		if ok {
			last = n
		}
		if ok && n-base >= 2 {
			break
		}
		if time.Now().After(deadline) {
			_, s, _ := mediaStatusOf(ctx, a, mediaID)
			t.Fatalf("the worker recorded %v unit(s) of media %s within 8 min, want 2 before the kill (processing_status=%s)",
				last-base, mediaID, s.ProcessingStatus)
		}
		time.Sleep(250 * time.Millisecond)
	}
	killed = true
	stopWorker(t, ctx, runLocal)
	if _, ok := workerRungsEncoded(ctx); ok {
		t.Fatalf("the media worker still answers on %s after SIGKILL", workerMetricsURL())
	}
	_, s, err := mediaStatusOf(ctx, a, mediaID)
	if err != nil {
		t.Fatalf("media status after the kill: %v", err)
	}
	if s.ProcessingStatus == "ready" {
		t.Fatalf("media %s was already ready when the worker was killed: the kill missed the transcode (raise ATPOST_E2E_KILL_FIXTURE_FPS)", mediaID)
	}
	t.Logf("killed the worker with %v unit(s) recorded; media %s is %s", last-base, mediaID, s.ProcessingStatus)

	// ── start it again ──
	startWorker(t, runLocal)
	restarted = true
	Eventually(t, 4*time.Minute, "the restarted worker logs 'Media transcode worker started'", func() bool {
		return serviceLogCount("[media-worker]", "Media transcode worker started") > startedBefore
	})

	// Kafka redelivers the uncommitted request once the dead member leaves
	// the group (its session timeout), so allow for that.
	waitMediaReady(t, ctx, a, mediaID, 10*time.Minute)

	t.Run("the lost run is counted", func(t *testing.T) {
		// cmd/worker/delivery.go beginDelivery.
		if !serviceLogContains(t, "Transcode of media "+mediaID+" restarting after") {
			t.Fatalf("no \"Transcode of media %s restarting after ... lost their worker\" line in %s", mediaID, serviceLogPath())
		}
	})
	t.Run("finished renditions reused", func(t *testing.T) {
		// cmd/worker/rungs.go reusable.
		if !serviceLogContains(t, "Rendition 360p of media "+mediaID+" reused from an earlier run") {
			t.Fatalf("no \"Rendition 360p of media %s reused from an earlier run\" line in %s: the restarted run encoded the finished 360p unit again",
				mediaID, serviceLogPath())
		}
	})

	waitPostPlayable(t, ctx, a, post.ID, 2*time.Minute)
	rungs := playAllRungs(t, ctx, b, mediaID)
	for _, want := range []string{"360p", "720p"} {
		if _, ok := rungs[want]; !ok {
			t.Fatalf("recovered ladder %v has no %s rung", keys(rungs), want)
		}
	}
}

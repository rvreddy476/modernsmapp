// Long video L3: watch-progress heartbeats at the real cadence: one write
// per viewer per 10 s for a concurrent audience.
// (long-video Track D section 6 L3; LV-D-6 / lv-22: every heartbeat runs
//  the full post read gate; LV-D-7 / lv-23: watch_progress never pruned)
//
// Endpoint: POST /v1/videos/:postId/progress  post-service handler.go:432
//   (SaveWatchProgress, video_series_handler.go). Auth: user.
//   Body {position_ms, duration_ms, sent_at_ms}. :videoId is the POST id.
// Model: VIEWERS (20,000) concurrent viewers, each holding one
//   (account, video) progress row and writing it every 10 s, so the arrival
//   rate is VIEWERS / 10 (2,000 writes/s). 5% of viewers have a second tab
//   open that writes the same row 3 s behind (tab=second), as the audit's
//   "2-tab share of 5%". Viewer k uses token k mod T and a video chosen so
//   each viewer has its own row; with fewer tokens than viewers, one
//   account watches several videos (rows and cadence stay right; the
//   per-account graph cache sees more traffic than in life; prefer
//   T >= VIEWERS).
// Rate: ramp 5 min, hold 30 min.
// Pass: p95 < 50 ms, errors < 0.1%. Also required, read outside k6:
//   post-service PG statements per write <= 2 (after LV-D-6), graph-service
//   rps from post-service < 10% of writes, Aurora CPU < 60%, and record the
//   watch_progress HOT-update ratio. Until lv-22 is fixed the 50 ms target
//   is expected to FAIL: that is the finding, not a broken script.
//
// Data: VIEWER_TOKENS_FILE (ideally 20,000 accounts); VIDEOS_FILE; the
//   audit's 50 million pre-existing watch_progress rows (seeding note in README).
// Writes: progress rows. ALLOW_WRITES=yes required.

import exec from 'k6/execution';
import { assertSafeTarget } from '../lib/guard.js';
import { tokens, tsv, scale, envInt, endpointThresholds } from '../lib/data.js';
import { saveProgress, V } from '../lib/longvideo.js';

const BASE = assertSafeTarget({ writes: true });
const viewers = tokens('VIEWER_TOKENS_FILE', 1);
const videos = tsv('VIDEOS_FILE', 1);
const VIEWERS = scale(envInt('VIEWERS', 20000));
const RATE = Math.max(1, Math.round(VIEWERS / 10));
const SECOND_TAB_EVERY = 20; // 1 in 20 viewers = 5%

export const options = {
  scenarios: {
    progress: {
      executor: 'ramping-arrival-rate',
      startRate: 1,
      timeUnit: '1s',
      preAllocatedVUs: Math.max(50, Math.round(RATE / 5)),
      maxVUs: Math.max(200, RATE),
      stages: [
        { duration: '5m', target: RATE },
        { duration: '30m', target: RATE },
        { duration: '1m', target: 0 },
      ],
    },
  },
  thresholds: Object.assign(
    endpointThresholds({ video_progress_save: ['p(95)<50'] }),
    {
      progress_errors: ['rate<0.001'],
      'http_req_failed{endpoint:video_progress_save}': ['rate<0.001'],
    },
  ),
  summaryTrendStats: ['avg', 'min', 'med', 'p(95)', 'p(99)', 'max'],
  tags: { suite: 'long-video', scenario_id: 'LV-L3' },
};

const startedAt = Date.now();

export default function () {
  const k = exec.scenario.iterationInTest % VIEWERS;
  const t = viewers.at(k);
  const row = videos[(k + Math.floor(k / viewers.length)) % videos.length];
  const dur = parseInt(row[V.dur], 10) || 3600000;
  // Each viewer started at a different point; position advances with time.
  const pos = ((k * 37000) + (Date.now() - startedAt)) % dur;
  saveProgress(BASE, t, row[V.post], pos, dur, 'first');
  if (k % SECOND_TAB_EVERY === 0) {
    saveProgress(BASE, t, row[V.post], Math.max(0, pos - 3000), dur, 'second');
  }
}

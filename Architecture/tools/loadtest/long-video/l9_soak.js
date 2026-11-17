// Long video L9: soak. L1 + L2 + L3 + L4 at 50% for SOAK_DURATION (6h).
// (long-video Track D section 6 L9)
//
// Same endpoints, data and per-request thresholds as l1_watch_open.js,
// l2_manifest_long.js, l3_progress.js and l4_analytics_long.js. The soak's
// own criteria are read outside k6: no memory growth > 10% per hour in
// post, media, feed or analytics-service. The 20 real browsers watching a
// 2 h video with a 40-minute pause (tab memory, resume position; LV-D-14)
// are a Playwright job, not k6 HTTP work; see README.

import { sleep } from 'k6';
import exec from 'k6/execution';
import { assertSafeTarget } from '../lib/guard.js';
import { tokens, tsv, vuToken, scale, pick, endpointThresholds } from '../lib/data.js';
import { watchOpen, saveProgress, V } from '../lib/longvideo.js';
import { startPlayback } from '../lib/hls.js';
import { analyticsBufferedTick } from '../lib/reels.js';

const BASE = assertSafeTarget({ writes: true });
const viewers = tokens('VIEWER_TOKENS_FILE', 1);
const videos = tsv('VIDEOS_FILE', 1);
const longs = tsv('LONG_VIDEOS_FILE', 1);
const DUR = String(__ENV.SOAK_DURATION || '6h');
const PROGRESS_VIEWERS = scale(10000);

function arrival(target, fn, vus) {
  return {
    executor: 'ramping-arrival-rate',
    exec: fn,
    startRate: 1,
    timeUnit: '1s',
    preAllocatedVUs: vus,
    maxVUs: vus * 4,
    stages: [
      { duration: '10m', target },
      { duration: DUR, target },
      { duration: '2m', target: 0 },
    ],
  };
}

export const options = {
  scenarios: {
    watch_open: arrival(scale(100), 'open', scale(150)),
    manifest: arrival(scale(218), 'manifest', scale(200)),
    progress: arrival(Math.round(PROGRESS_VIEWERS / 10), 'progress', scale(100)),
    analytics: {
      executor: 'ramping-vus',
      exec: 'analytics',
      startVUs: 0,
      stages: [
        { duration: '10m', target: scale(10000) },
        { duration: DUR, target: scale(10000) },
        { duration: '2m', target: 0 },
      ],
    },
  },
  thresholds: Object.assign(
    endpointThresholds({
      post_get: ['p(95)<300', 'p(99)<800'],
      video_detail: ['p(95)<300', 'p(99)<800'],
      related: ['p(95)<500', 'p(99)<800'],
      comments_top: ['p(95)<300', 'p(99)<800'],
      hls_master: ['p(95)<200'],
      hls_child: ['p(95)<200'],
      video_progress_save: ['p(95)<50'],
      analytics_events: ['p(95)<200'],
    }),
    {
      watch_open_errors: ['rate<0.005'],
      hls_503_total: ['count<1'],
      progress_errors: ['rate<0.001'],
      analytics_errors: ['rate<0.001'],
    },
  ),
  summaryTrendStats: ['avg', 'min', 'med', 'p(95)', 'p(99)', 'max'],
  tags: { suite: 'long-video', scenario_id: 'LV-L9' },
};

const startedAt = Date.now();

export function open() {
  watchOpen(BASE, vuToken(viewers), pick(videos));
}

export function manifest() {
  const t = vuToken(viewers);
  const row = pick(longs);
  const children = startPlayback(BASE, t, row[V.media], { tags: { flow: 'long' } });
  if (children.length > 1 && Math.random() < 0.3) {
    startPlayback(BASE, t, row[V.media], { childIndex: 1, tags: { flow: 'long', switch: 'yes' } });
  }
}

export function progress() {
  const k = exec.scenario.iterationInTest % PROGRESS_VIEWERS;
  const row = videos[(k + Math.floor(k / viewers.length)) % videos.length];
  const dur = parseInt(row[V.dur], 10) || 3600000;
  saveProgress(BASE, viewers.at(k), row[V.post], (k * 37000 + (Date.now() - startedAt)) % dur, dur, 'first');
}

export function analytics() {
  const web = exec.vu.idInTest % 2 === 0;
  analyticsBufferedTick(BASE, vuToken(viewers), videos, 'posttube', web ? 10 : 60, 0, web ? 'web' : 'android');
  sleep(5);
}

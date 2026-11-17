// Long video L5: long-transcode lane occupancy, with the reels burst on top.
// (long-video Track D section 6 L5; lv-4: two long lanes for the platform,
//  sharing CPU with reels; lv-5 / lv-6 restarts)
//
// Endpoints (media-service; file:line in ../lib/upload.js):
//   POST /v1/media/upload/resumable/init | :id/chunk?part_number=N | :id/complete
//   POST /v1/media/init, PUT <upload_url>, POST /v1/media/confirm  (reel probe)
//   GET  /v1/media/:mediaId/status  polled as the uploader
// Load: at t=0, LONG_1H_JOBS (4) uploads of LONG_1H_SAMPLE_FILE (a real
//   1 h 1080p video) and LONG_4H_JOBS (2) of LONG_4H_SAMPLE_FILE (4 h
//   1080p), optionally LONG_4K_JOBS (1) of LONG_4K_SAMPLE_FILE (4 h 4K,
//   ~16 GiB). From t=REEL_BURST_START (10m): 200 short reels
//   (SAMPLE_VIDEO_FILE) in 5 minutes, as reels L4.
// Lane occupancy: transcode_queue_age_ms{class:long_*} is how long each
//   long job waited for a lane; with two lanes, jobs 3+ wait for a whole
//   encode. LOG_TIMELINE=1 prints one TIMELINE line per job (confirmed,
//   started, finished) so the README's one-liner rebuilds how many ran at once.
// Pass: reel p95 confirm-to-ready < 120 s while long jobs run; 1 h 1080p
//   ready < 6 h; no terminal failure. Also required, read outside k6:
//   scratch peak < 80% of the volume, media.events.long consumer lag back
//   to 0, per-job wall time recorded (from TIMELINE).
// Duration: up to LONG_MAX_WAIT_S (default 12 h) per long job.
//
// Data: UPLOADER_TOKENS_FILE with >= 21 accounts (upload budget 10/h);
//   media.events.long must exist in the target's Kafka (map question 2),
//   or long jobs silently share the short lane.
// Writes: ALLOW_WRITES=yes required.

import exec from 'k6/execution';
import { assertSafeTarget } from '../lib/guard.js';
import { tokens, scale, envInt } from '../lib/data.js';
import { sampleFile, simpleUpload, resumableUpload, waitForTranscode } from '../lib/upload.js';

const BASE = assertSafeTarget({ writes: true });

function has(name) {
  return String(__ENV[name] || '').trim() !== '';
}

const J1 = has('LONG_1H_SAMPLE_FILE') ? envInt('LONG_1H_JOBS', 4) : 0;
const J4 = has('LONG_4H_SAMPLE_FILE') ? envInt('LONG_4H_JOBS', 2) : 0;
const JK = has('LONG_4K_SAMPLE_FILE') ? envInt('LONG_4K_JOBS', 1) : 0;
if (J1 + J4 + JK === 0) {
  throw new Error('set at least LONG_1H_SAMPLE_FILE (see README, scenario LV-L5)');
}
const REELS = has('SAMPLE_VIDEO_FILE') ? scale(envInt('SHORT_UPLOADS', 200)) : 0;
const uploaders = tokens('UPLOADER_TOKENS_FILE', Math.ceil((J1 + J4 + JK + REELS) / 10));
const s1 = J1 ? sampleFile('LONG_1H_SAMPLE_FILE') : null;
const s4 = J4 ? sampleFile('LONG_4H_SAMPLE_FILE') : null;
const sk = JK ? sampleFile('LONG_4K_SAMPLE_FILE') : null;
const sr = REELS ? sampleFile('SAMPLE_VIDEO_FILE') : null;
const LONG_WAIT = envInt('LONG_MAX_WAIT_S', 43200);

const scenarios = {};
let tokenBase = 0;
function longScenario(name, jobs, fn) {
  if (!jobs) return;
  scenarios[name] = {
    executor: 'per-vu-iterations',
    exec: fn,
    vus: jobs,
    iterations: 1,
    maxDuration: `${Math.ceil(LONG_WAIT / 3600) + 2}h`,
    env: { TOKEN_BASE: String(tokenBase) },
  };
  tokenBase += jobs;
}
longScenario('long_1h', J1, 'long1h');
longScenario('long_4h', J4, 'long4h');
longScenario('long_4k', JK, 'long4k');
if (REELS) {
  scenarios.reel_probe = {
    executor: 'constant-arrival-rate',
    exec: 'reel',
    startTime: String(__ENV.REEL_BURST_START || '10m'),
    rate: REELS,
    timeUnit: '5m',
    duration: '5m',
    preAllocatedVUs: Math.min(REELS + 10, 260),
    maxVUs: REELS + 20,
    gracefulStop: '17m',
    env: { TOKEN_BASE: String(tokenBase) },
  };
}

const thresholds = {
  transcode_failed_total: ['count<1'],
  upload_errors: ['rate<0.01'],
};
if (J1) thresholds['transcode_confirm_to_ready_ms{class:long_1h}'] = ['max<21600000'];
if (REELS) thresholds['transcode_confirm_to_ready_ms{class:short}'] = ['p(95)<120000'];

export const options = {
  scenarios,
  thresholds,
  summaryTrendStats: ['avg', 'min', 'med', 'p(95)', 'p(99)', 'max'],
  tags: { suite: 'long-video', scenario_id: 'LV-L5' },
};

async function longJob(holder, cls) {
  const t = uploaders.at(parseInt(__ENV.TOKEN_BASE, 10) + exec.scenario.iterationInTest);
  const up = await resumableUpload(BASE, t, holder, { class: cls });
  if (!up) return;
  waitForTranscode(BASE, t, up, { maxWaitS: LONG_WAIT, pollS: 60, metricTags: { class: cls }, tags: { class: cls } });
}

export async function long1h() {
  await longJob(s1, 'long_1h');
}
export async function long4h() {
  await longJob(s4, 'long_4h');
}
export async function long4k() {
  await longJob(sk, 'long_4k');
}

export async function reel() {
  const t = uploaders.at(parseInt(__ENV.TOKEN_BASE, 10) + exec.scenario.iterationInTest);
  const up = await simpleUpload(BASE, t, sr, { class: 'short' });
  if (!up) return;
  waitForTranscode(BASE, t, up, { maxWaitS: 900, metricTags: { class: 'short' }, tags: { class: 'short' } });
}

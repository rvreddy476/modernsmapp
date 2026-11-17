// Reels L4: transcode throughput. N uploads queued in a burst; queue age
// and confirm-to-ready measured by polling the status route.
// (reels Track D section 6 L4, reels-11; findings D-3 / two short lanes)
//
// Endpoints (media-service; see ../lib/upload.js for file:line):
//   POST /v1/media/init, PUT <presigned upload_url>, POST /v1/media/confirm
//   POST /v1/media/upload/resumable/init|:id/chunk|:id/complete (4K jobs)
//   GET  /v1/media/:mediaId/status   polled as the uploader every POLL_INTERVAL_S
// Load: 200 short reels (SAMPLE_VIDEO_FILE: a real 30-60 s 1080x1920 MP4,
//   20-60 MB) confirmed over 5 minutes, plus LARGE_JOBS (3) 10-minute 4K
//   portrait reels (LARGE_REEL_SAMPLE_FILE, optional) confirmed at t=0.
// Pass (thresholds below): p95 confirm-to-ready < 120 s for the short
//   reels; no item still processing 15 min after confirm (MAX_WAIT_S=900,
//   transcode_stuck_total = 0); no failed or rejected transcode. Also
//   required, read outside k6: media_transcode_quarantine count 0, consumer
//   lag (media-transcode-worker, post-service-media-transcode) back to 0
//   within 20 min.
// Queue age = confirm until the worker has started a rendition job (the
//   first transcoding_jobs row past "queued").
//
// Data: UPLOADER_TOKENS_FILE with at least ceil(uploads / 10) accounts:
//   init counts against 10 uploads per hour and 30 per day per account
//   (media-service service/ratelimit.go). One account per upload (203) is
//   the safe size and allows same-day reruns.
// Writes: uploads real objects and media rows. ALLOW_WRITES=yes required.

import exec from 'k6/execution';
import { assertSafeTarget } from '../lib/guard.js';
import { tokens, scale, envInt } from '../lib/data.js';
import { sampleFile, simpleUpload, resumableUpload, waitForTranscode } from '../lib/upload.js';

const BASE = assertSafeTarget({ writes: true });
const SHORT_UPLOADS = scale(envInt('SHORT_UPLOADS', 200));
const LARGE_ENABLED = String(__ENV.LARGE_REEL_SAMPLE_FILE || '').trim() !== '';
const LARGE_JOBS = LARGE_ENABLED ? envInt('LARGE_JOBS', 3) : 0;
const TOTAL = SHORT_UPLOADS + LARGE_JOBS;
const uploaders = tokens('UPLOADER_TOKENS_FILE', Math.ceil(TOTAL / 10));
const shortSample = sampleFile('SAMPLE_VIDEO_FILE');
const largeSample = LARGE_ENABLED ? sampleFile('LARGE_REEL_SAMPLE_FILE') : null;

const scenarios = {
  short_burst: {
    executor: 'constant-arrival-rate',
    exec: 'short',
    rate: SHORT_UPLOADS,
    timeUnit: '5m',
    duration: '5m',
    preAllocatedVUs: Math.min(SHORT_UPLOADS + 10, 260),
    maxVUs: SHORT_UPLOADS + 20,
    gracefulStop: '17m',
  },
};
if (LARGE_ENABLED) {
  scenarios.large_4k = {
    executor: 'per-vu-iterations',
    exec: 'large',
    vus: LARGE_JOBS,
    iterations: 1,
    maxDuration: '60m',
  };
}

export const options = {
  scenarios,
  thresholds: {
    'transcode_confirm_to_ready_ms{class:short}': ['p(95)<120000'],
    transcode_stuck_total: ['count<1'],
    transcode_failed_total: ['count<1'],
    upload_errors: ['rate<0.01'],
  },
  summaryTrendStats: ['avg', 'min', 'med', 'p(95)', 'p(99)', 'max'],
  tags: { suite: 'reels', scenario_id: 'R-L4' },
};

export async function short() {
  const t = uploaders.at(exec.scenario.iterationInTest);
  const up = await simpleUpload(BASE, t, shortSample, { class: 'short' });
  if (!up) return;
  waitForTranscode(BASE, t, up, { maxWaitS: envInt('MAX_WAIT_S', 900), metricTags: { class: 'short' }, tags: { class: 'short' } });
}

export async function large() {
  const t = uploaders.at(SHORT_UPLOADS + exec.vu.idInTest);
  const up = await resumableUpload(BASE, t, largeSample, { class: 'large_4k' });
  if (!up) return;
  waitForTranscode(BASE, t, up, { maxWaitS: envInt('MAX_WAIT_S', 900), pollS: 15, metricTags: { class: 'large_4k' }, tags: { class: 'large_4k' } });
}

// Reels L5: the upload path under load: simple init/PUT/confirm and
// resumable init/part/complete, optionally while playback (L2) runs at 50%.
// (reels Track D section 6 L5, finding D-15; reels-13)
//
// Endpoints (media-service; file:line in ../lib/upload.js):
//   POST /v1/media/init, PUT <upload_url>, POST /v1/media/confirm        user
//   POST /v1/media/upload/resumable/init                                 user
//   POST /v1/media/upload/resumable/:uploadId/chunk?part_number=N        user
//   POST /v1/media/upload/resumable/:uploadId/complete                   user
// Part size is chosen by the SERVER (service/resumable.go:75
//   resumableChunkPlan): ceil(total/9000) rounded up to a MiB, floor 5 MiB,
//   cap 64 MiB. A 1 GB file is 205 parts of 5 MiB; the "64 MiB parts" in
//   the audit's L5 row only happens past ~560 GiB, so this script follows
//   the server's plan as every client does.
// Bytes: UPLOAD_SAMPLE_FILE (a real video; 1 GB for the audit's L5) or,
//   when unset, SYNTHETIC_MB of zero bytes (default 12: three small parts).
//   Synthetic content is refused at complete (400, counted as expected), so
//   it measures init/part/complete cost without queueing a transcode.
// Load: RESUMABLE_UPLOADS (50) concurrent resumable uploads, SIMPLE_RATE
//   (0 by default) simple uploads per minute, and with WITH_PLAYBACK=1 the
//   L2 playback viewers at 50% (1,500).
// Pass: part error rate < 1%; with playback, L2's thresholds still hold
//   (/hls/ p95 < 150 ms, zero 503s). Also required, read outside k6:
//   media-service memory < 70% of its limit.
//
// Data: UPLOADER_TOKENS_FILE (>= uploads/10 accounts; one per upload is
//   safest); for WITH_PLAYBACK also VIEWER_TOKENS_FILE and REELS_FILE.
// Writes: ALLOW_WRITES=yes required.

import { sleep } from 'k6';
import exec from 'k6/execution';
import { assertSafeTarget } from '../lib/guard.js';
import { tokens, tsv, vuToken, scale, envInt, endpointThresholds } from '../lib/data.js';
import { sampleFile, syntheticFile, resumableUpload, simpleUpload } from '../lib/upload.js';
import { playbackIteration } from '../lib/reels.js';

const BASE = assertSafeTarget({ writes: true });
const RESUMABLE = scale(envInt('RESUMABLE_UPLOADS', 50));
const SIMPLE_RATE = envInt('SIMPLE_RATE', 0);
const WITH_PLAYBACK = String(__ENV.WITH_PLAYBACK || '') === '1';
const uploaders = tokens('UPLOADER_TOKENS_FILE', Math.ceil((RESUMABLE + SIMPLE_RATE * 20) / 10));
const holder = String(__ENV.UPLOAD_SAMPLE_FILE || '').trim()
  ? sampleFile('UPLOAD_SAMPLE_FILE')
  : syntheticFile(envInt('SYNTHETIC_MB', 12) * 1024 * 1024);
const viewers = WITH_PLAYBACK ? tokens('VIEWER_TOKENS_FILE', 1) : null;
const reels = WITH_PLAYBACK ? tsv('REELS_FILE', 100) : null;

const scenarios = {
  resumable_uploads: {
    executor: 'per-vu-iterations',
    exec: 'resumable',
    vus: RESUMABLE,
    iterations: 1,
    maxDuration: '90m',
  },
};
if (SIMPLE_RATE > 0) {
  scenarios.simple_uploads = {
    executor: 'constant-arrival-rate',
    exec: 'simple',
    rate: SIMPLE_RATE,
    timeUnit: '1m',
    duration: '20m',
    preAllocatedVUs: 20,
    maxVUs: 100,
  };
}
if (WITH_PLAYBACK) {
  scenarios.playback_half = {
    executor: 'ramping-vus',
    exec: 'playback',
    startVUs: 0,
    stages: [
      { duration: '5m', target: scale(1500) },
      { duration: '30m', target: scale(1500) },
      { duration: '1m', target: 0 },
    ],
  };
}

const thresholds = {
  upload_part_errors: ['rate<0.01'],
  upload_errors: ['rate<0.01'],
};
if (WITH_PLAYBACK) {
  Object.assign(thresholds, endpointThresholds({ hls_master: ['p(95)<150'], hls_child: ['p(95)<150'] }), {
    hls_503_total: ['count<1'],
  });
}

export const options = {
  scenarios,
  thresholds,
  summaryTrendStats: ['avg', 'min', 'med', 'p(95)', 'p(99)', 'max'],
  tags: { suite: 'reels', scenario_id: 'R-L5' },
};

export async function resumable() {
  const t = uploaders.at(exec.vu.idInTest - 1);
  await resumableUpload(BASE, t, holder, { class: 'resumable' });
}

export async function simple() {
  if (holder.synthetic) return; // the simple path needs real bytes for confirm
  const t = uploaders.at(RESUMABLE + exec.scenario.iterationInTest);
  await simpleUpload(BASE, t, holder, { class: 'simple' });
}

export function playback() {
  playbackIteration(BASE, vuToken(viewers), reels);
  sleep(4 * (0.75 + Math.random() * 0.5));
}

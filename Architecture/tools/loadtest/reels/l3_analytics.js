// Reels L3: view and watch-time event ingest.  (reels Track D section 6)
//
// Endpoint: POST /v1/analytics/events  (analytics-service handler.go:93,
//   IngestEvents :166). Auth: user. See ../lib/analytics.js for the body.
// Shapes: web, one event per request, a heartbeat every 5 s per viewer
//   (WEB_SHARE, default 0.4 of viewers); Android, events buffered and sent
//   as one batch of 50 to 100 every ANDROID_FLUSH_S (60) seconds.
// Rate: 10,000 concurrent viewers.
// Pass: p95 < 200 ms, errors < 0.1%. Also required, read outside k6:
//   Aurora write latency p95 < 20 ms, no lock waits on
//   analytics.playback_sessions.
//
// Data: VIEWER_TOKENS_FILE; REELS_FILE. Every post_id must already be
//   projected in analytics (the PostCreated ownership projection), or the
//   batch is a 422 CONTENT_NOT_READY (counted in analytics_content_not_ready).
// 10,000 VUs is more than one load generator holds comfortably: run it
//   distributed or with RATE_SCALE (README, "Generator size").

import { sleep } from 'k6';
import { assertSafeTarget } from '../lib/guard.js';
import { tokens, tsv, vuToken, scale, envFloat, envInt, endpointThresholds } from '../lib/data.js';
import { analyticsWebTick, analyticsBufferedTick } from '../lib/reels.js';

const BASE = assertSafeTarget({ writes: true });
const viewers = tokens('VIEWER_TOKENS_FILE', 1);
const reels = tsv('REELS_FILE', 100);
const WEB_SHARE = envFloat('WEB_SHARE', 0.4);
const FLUSH_S = envInt('ANDROID_FLUSH_S', 60);
const TOTAL = scale(10000);
const WEB = Math.max(1, Math.round(TOTAL * WEB_SHARE));
const ANDROID = Math.max(1, TOTAL - WEB);

function ramp(target) {
  return {
    executor: 'ramping-vus',
    startVUs: 0,
    stages: [
      { duration: '5m', target },
      { duration: '20m', target },
      { duration: '1m', target: 0 },
    ],
    gracefulRampDown: '30s',
  };
}

export const options = {
  scenarios: {
    analytics_web: Object.assign(ramp(WEB), { exec: 'web' }),
    analytics_android: Object.assign(ramp(ANDROID), { exec: 'android' }),
  },
  thresholds: Object.assign(
    endpointThresholds({ analytics_events: ['p(95)<200'] }),
    {
      analytics_errors: ['rate<0.001'],
      'http_req_failed{endpoint:analytics_events}': ['rate<0.001'],
    },
  ),
  summaryTrendStats: ['avg', 'min', 'med', 'p(95)', 'p(99)', 'max'],
  tags: { suite: 'reels', scenario_id: 'R-L3' },
};

export function web() {
  analyticsWebTick(BASE, vuToken(viewers), reels, 'reels');
  sleep(5);
}

export function android() {
  analyticsBufferedTick(BASE, vuToken(viewers), reels, 'reels', FLUSH_S, 0.25, 'android');
  sleep(5);
}

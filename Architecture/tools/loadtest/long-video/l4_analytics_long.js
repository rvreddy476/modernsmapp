// Long video L4: analytics for long sessions.  (long-video Track D section 6 L4)
//
// Endpoint: POST /v1/analytics/events  (analytics-service handler.go:93). user.
//   Body built by ../lib/analytics.js; surface "posttube"; content_id = post id.
// Model: VIEWERS (20,000) concurrent viewers, a heartbeat every 5 s of
//   watching. Web shape: buffered and sent every 10 s (2 heartbeats per
//   request); Android shape: buffered and sent every ANDROID_FLUSH_S (60 s,
//   about 12 events; Android caps a batch at 100). Sessions end only at the
//   end of the video. WEB_SHARE (0.5) of viewers are web.
// Rate: about 4,000 events/s. Ramp 5 min, hold 20 min.
// Pass: p95 < 200 ms, errors < 0.1%. Also required, read outside k6: no
//   lock waits on analytics.playback_sessions.
//
// Data: VIEWER_TOKENS_FILE; VIDEOS_FILE (post ids projected in analytics).
// 20,000 VUs needs a distributed run or RATE_SCALE (README).

import { sleep } from 'k6';
import { assertSafeTarget } from '../lib/guard.js';
import { tokens, tsv, vuToken, scale, envInt, envFloat, endpointThresholds } from '../lib/data.js';
import { analyticsBufferedTick } from '../lib/reels.js';

const BASE = assertSafeTarget({ writes: true });
const viewers = tokens('VIEWER_TOKENS_FILE', 1);
const videos = tsv('VIDEOS_FILE', 1);
const TOTAL = scale(envInt('VIEWERS', 20000));
const WEB = Math.max(1, Math.round(TOTAL * envFloat('WEB_SHARE', 0.5)));
const ANDROID = Math.max(1, TOTAL - WEB);
const FLUSH_S = envInt('ANDROID_FLUSH_S', 60);

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
  thresholds: Object.assign(endpointThresholds({ analytics_events: ['p(95)<200'] }), {
    analytics_errors: ['rate<0.001'],
  }),
  summaryTrendStats: ['avg', 'min', 'med', 'p(95)', 'p(99)', 'max'],
  tags: { suite: 'long-video', scenario_id: 'LV-L4' },
};

// VIDEOS_FILE has the duration in column 2 like REELS_FILE, and post_id in
// column 0, so the reels tick reads it unchanged.
export function web() {
  analyticsBufferedTick(BASE, vuToken(viewers), videos, 'posttube', 10, 0, 'web');
  sleep(5);
}

export function android() {
  analyticsBufferedTick(BASE, vuToken(viewers), videos, 'posttube', FLUSH_S, 0, 'android');
  sleep(5);
}

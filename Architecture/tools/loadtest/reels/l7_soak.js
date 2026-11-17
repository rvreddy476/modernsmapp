// Reels L7: soak. L1 + L2 + L3 at 50% for SOAK_DURATION (4h).
// (reels Track D section 6 L7)
//
// Same endpoints, data and per-request thresholds as l1_feed.js,
// l2_playback.js and l3_analytics.js. The soak's own pass criteria are read
// outside k6: no memory growth > 10% per hour in media-service,
// feed-service or analytics-service, and events_raw partition routing
// correct (reels D-12). k6 enforces that the latency targets still hold
// at hour 4.
// (L6, 500 real browsers on /reels, is not k6 HTTP work: it needs
//  Playwright or k6 browser and a Next server egress measurement; see README.)

import { sleep } from 'k6';
import { assertSafeTarget } from '../lib/guard.js';
import { tokens, tsv, vuToken, scale, endpointThresholds } from '../lib/data.js';
import { feedIteration, playbackIteration, analyticsWebTick, analyticsBufferedTick, loadCategories } from '../lib/reels.js';

const BASE = assertSafeTarget({ writes: true });
const viewers = tokens('VIEWER_TOKENS_FILE', 1);
const reels = tsv('REELS_FILE', 100);
const DUR = String(__ENV.SOAK_DURATION || '4h');

function hold(target) {
  return {
    executor: 'ramping-vus',
    startVUs: 0,
    stages: [
      { duration: '10m', target },
      { duration: DUR, target },
      { duration: '2m', target: 0 },
    ],
    gracefulRampDown: '30s',
  };
}

export const options = {
  scenarios: {
    feed: {
      executor: 'ramping-arrival-rate',
      exec: 'feed',
      startRate: 1,
      timeUnit: '1s',
      preAllocatedVUs: scale(250),
      maxVUs: scale(750),
      stages: [
        { duration: '10m', target: scale(100) },
        { duration: DUR, target: scale(100) },
        { duration: '2m', target: 0 },
      ],
    },
    playback: Object.assign(hold(scale(1500)), { exec: 'playback' }),
    analytics_web: Object.assign(hold(scale(2000)), { exec: 'web' }),
    analytics_android: Object.assign(hold(scale(3000)), { exec: 'android' }),
  },
  thresholds: Object.assign(
    endpointThresholds({
      feed_reels: ['p(95)<300', 'p(99)<800'],
      hls_master: ['p(95)<150'],
      hls_child: ['p(95)<150'],
      analytics_events: ['p(95)<200'],
    }),
    {
      feed_errors: ['rate<0.005'],
      hls_503_total: ['count<1'],
      analytics_errors: ['rate<0.001'],
    },
  ),
  summaryTrendStats: ['avg', 'min', 'med', 'p(95)', 'p(99)', 'max'],
  tags: { suite: 'reels', scenario_id: 'R-L7' },
};

export function setup() {
  return { cats: loadCategories(BASE) };
}

export function feed(data) {
  feedIteration(BASE, vuToken(viewers), data.cats);
}

export function playback() {
  playbackIteration(BASE, vuToken(viewers), reels);
  sleep(4 * (0.75 + Math.random() * 0.5));
}

export function web() {
  analyticsWebTick(BASE, vuToken(viewers), reels, 'reels');
  sleep(5);
}

export function android() {
  analyticsBufferedTick(BASE, vuToken(viewers), reels, 'reels', 60, 0.25, 'android');
  sleep(5);
}

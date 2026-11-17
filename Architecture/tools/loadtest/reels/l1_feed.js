// Reels L1: reels feed pages.  (reels Track D section 6, finding reels-11)
//
// Endpoint: GET /v1/feed/reels?limit=15[&following_only=true|&category=<slug>][&cursor=]
//   feed-service internal/http/handler.go:94 (GetReelFeed :261). Auth: user.
// Mix: 70% plain, 20% following_only, 10% category (slugs from
//   GET /v1/posts/categories?kind=short, or REEL_CATEGORIES=a,b,c).
//   Half of the opens also read the next page (meta.next_cursor).
// Rate: ramp to 300 requests/s over 10 min, hold 20 min. One iteration is
//   1.5 requests on average, so the arrival rate peaks at 200 iterations/s.
// Pass: p95 < 300 ms, p99 < 800 ms, errors < 0.5%.
//   feed-service CPU < 70% is read from the cluster, not from k6.
//
// Data: VIEWER_TOKENS_FILE (>= 1,000 accounts, each following 50-500
//   authors so following_only and the home timelines have rows).
// Run: see ../README.md, scenario R-L1.

import { assertSafeTarget } from '../lib/guard.js';
import { tokens, vuToken, scale, endpointThresholds } from '../lib/data.js';
import { feedIteration, loadCategories } from '../lib/reels.js';

const BASE = assertSafeTarget({ writes: false });
const viewers = tokens('VIEWER_TOKENS_FILE', 1);

const PEAK_ITER = scale(200);

export const options = {
  scenarios: {
    reels_feed: {
      executor: 'ramping-arrival-rate',
      startRate: 1,
      timeUnit: '1s',
      preAllocatedVUs: scale(500),
      maxVUs: scale(1500),
      stages: [
        { duration: '10m', target: PEAK_ITER },
        { duration: '20m', target: PEAK_ITER },
        { duration: '1m', target: 0 },
      ],
    },
  },
  thresholds: Object.assign(
    endpointThresholds({ feed_reels: ['p(95)<300', 'p(99)<800'] }),
    {
      'http_req_failed{endpoint:feed_reels}': ['rate<0.005'],
      feed_errors: ['rate<0.005'],
    },
  ),
  summaryTrendStats: ['avg', 'min', 'med', 'p(95)', 'p(99)', 'max'],
  tags: { suite: 'reels', scenario_id: 'R-L1' },
};

export function setup() {
  return { cats: loadCategories(BASE) };
}

export default function (data) {
  feedIteration(BASE, vuToken(viewers), data.cats);
}

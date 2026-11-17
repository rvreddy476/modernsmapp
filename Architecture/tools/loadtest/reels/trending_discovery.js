// Reels trending and discovery reads.  (Not one of the audit's L1-L7 rows;
// asked for by the load-tests lane. Thresholds are PROVISIONAL, borrowed
// from L1: p95 < 300 ms, p99 < 800 ms, errors < 0.5%.)
//
// Endpoints (verified in code):
//   GET /v1/posts/trending?content_type=flick&limit=20[&cursor]
//       post-service handler.go:284 (hashtag_trending_handler.go GetTrendingPosts). public.
//   GET /v1/reels/hashtags/trending?limit=20&days=7
//       post-service reel_discovery.go:15. public.
//   GET /v1/discover/trending
//       search-service internal/http/handler.go:187, gateway prefix
//       /v1/discover (api-gateway main.go:668). The reels map's
//       "/v1/search/discover/trending" does not exist.
// Mix: 40% trending reels (30% of those read page 2), 30% hashtags, 30% discover.
// Rate: ramp to TRENDING_RPS (100) requests/s over 5 min, hold 15 min.
//
// Data: VIEWER_TOKENS_FILE. The trending window needs reels with likes and
//   views in the last 7 days (run engagement_writes.js and l3_analytics.js
//   first, or seed them), otherwise the reads are cheap and prove nothing.

import { assertSafeTarget } from '../lib/guard.js';
import { tokens, vuToken, scale, envInt, endpointThresholds } from '../lib/data.js';
import { trendingIteration } from '../lib/reels.js';

const BASE = assertSafeTarget({ writes: false });
const viewers = tokens('VIEWER_TOKENS_FILE', 1);
const RPS = scale(envInt('TRENDING_RPS', 100));

export const options = {
  scenarios: {
    trending: {
      executor: 'ramping-arrival-rate',
      startRate: 1,
      timeUnit: '1s',
      preAllocatedVUs: Math.max(20, RPS),
      maxVUs: Math.max(100, RPS * 4),
      stages: [
        { duration: '5m', target: RPS },
        { duration: '15m', target: RPS },
        { duration: '1m', target: 0 },
      ],
    },
  },
  thresholds: Object.assign(
    endpointThresholds({
      posts_trending: ['p(95)<300', 'p(99)<800'],
      reels_hashtags_trending: ['p(95)<300', 'p(99)<800'],
      discover_trending: ['p(95)<300', 'p(99)<800'],
    }),
    { trending_errors: ['rate<0.005'] },
  ),
  summaryTrendStats: ['avg', 'min', 'med', 'p(95)', 'p(99)', 'max'],
  tags: { suite: 'reels', scenario_id: 'R-TREND' },
};

export default function () {
  trendingIteration(BASE, vuToken(viewers));
}

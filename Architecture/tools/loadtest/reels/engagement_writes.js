// Reels engagement writes: like, comment, follow.  (Not one of the audit's
// L1-L7 rows; asked for by the load-tests lane. Thresholds are PROVISIONAL,
// borrowed from L1's read targets, until the founder or the audit sets
// write targets.)
//
// Endpoints (verified in code; see ../lib/reels.js header for file:line):
//   POST /v1/posts/:postId/like                 post-service. user. TOGGLE.
//   POST /v1/posts/:postId/comments {text}      post-service. user.
//        Idempotency-Key sent; per-user comment rate limit -> 429.
//   POST /v1/graph/follow   {user_id}           graph-service. user.
//   POST /v1/graph/unfollow {user_id}           graph-service. user.
// Mix: 60% like toggle, 25% comment, 15% follow/unfollow of the reel's author.
// Rate: ramp to WRITE_RPS (100) writes/s over 5 min, hold 15 min.
// Pass (provisional): each endpoint p95 < 300 ms, p99 < 800 ms; errors < 0.5%.
//   429s are counted separately (engagement_429_total): with enough
//   accounts there should be none; if there are, the run is invalid, add
//   accounts rather than reading it as a pass or a fail.
//
// Data: VIEWER_TOKENS_FILE (>= 2,000 accounts so per-user limits are not
//   the bottleneck); REELS_FILE with author_id filled in.
// Writes: creates comments, likes and follow edges. ALLOW_WRITES=yes required.

import { assertSafeTarget } from '../lib/guard.js';
import { tokens, tsv, vuToken, scale, envInt, endpointThresholds } from '../lib/data.js';
import { engagementIteration } from '../lib/reels.js';

const BASE = assertSafeTarget({ writes: true });
const viewers = tokens('VIEWER_TOKENS_FILE', 1);
const reels = tsv('REELS_FILE', 100);
const RPS = scale(envInt('WRITE_RPS', 100));

export const options = {
  scenarios: {
    engagement: {
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
      post_like: ['p(95)<300', 'p(99)<800'],
      post_comment: ['p(95)<300', 'p(99)<800'],
      graph_follow: ['p(95)<300', 'p(99)<800'],
      graph_unfollow: ['p(95)<300', 'p(99)<800'],
    }),
    { engagement_errors: ['rate<0.005'] },
  ),
  summaryTrendStats: ['avg', 'min', 'med', 'p(95)', 'p(99)', 'max'],
  tags: { suite: 'reels', scenario_id: 'R-ENG' },
};

export default function () {
  engagementIteration(BASE, vuToken(viewers), reels);
}

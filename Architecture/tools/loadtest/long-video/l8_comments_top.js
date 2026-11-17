// Long video L8: comments on a 50,000-comment video, "top" and "newest",
// pages 1 to 10, with replies.  (long-video Track D section 6 L8; lv-24:
// "top" counts reactions per comment on every page, with OFFSET)
//
// Endpoints:
//   GET /v1/posts/:postId/comments?sort=top|newest&limit=20&cursor=
//       post-service handler.go:147 (ListComments, comments_handler.go). public.
//       Any other sort value is a 400.
//   GET /v1/comments/:commentId/replies?limit=20
//       post-service handler.go:327 (ListReplies). public.
// Model: each VU walks one sort order 1 to 10 pages deep, one page per
//   iteration, following meta.next_cursor; on 30% of pages it opens the
//   replies of a comment whose reply_count > 0.
// Rate: COMMENTS_RPS (100) page reads/s (replies add about 30% on top).
//   Ramp 3 min, hold 15 min.
// Pass: p95 < 150 ms for both sorts at every depth; errors < 0.5%
//   (error rate provisional). Per-page latency is tagged page=1..10, so a
//   cost that grows with OFFSET shows in the summary per page.
//
// Data: VIEWER_TOKENS_FILE; COMMENT_VIDEO_POST_ID = the post id of the video
//   seeded with 50,000 top-level comments, 200,000 reactions and replies.

import { assertSafeTarget } from '../lib/guard.js';
import { tokens, vuToken, scale, envInt, requireEnv, endpointThresholds } from '../lib/data.js';
import { commentsIteration } from '../lib/longvideo.js';

const BASE = assertSafeTarget({ writes: false });
const viewers = tokens('VIEWER_TOKENS_FILE', 1);
const POST = requireEnv('COMMENT_VIDEO_POST_ID');
const RPS = scale(envInt('COMMENTS_RPS', 100));

export const options = {
  scenarios: {
    comments: {
      executor: 'ramping-arrival-rate',
      startRate: 1,
      timeUnit: '1s',
      preAllocatedVUs: Math.max(20, RPS),
      maxVUs: Math.max(100, RPS * 4),
      stages: [
        { duration: '3m', target: RPS },
        { duration: '15m', target: RPS },
        { duration: '1m', target: 0 },
      ],
    },
  },
  thresholds: Object.assign(
    endpointThresholds({
      comments_top: ['p(95)<150'],
      comments_newest: ['p(95)<150'],
      comment_replies: ['p(95)<150'],
    }),
    { comments_errors: ['rate<0.005'] },
  ),
  summaryTrendStats: ['avg', 'min', 'med', 'p(95)', 'p(99)', 'max'],
  tags: { suite: 'long-video', scenario_id: 'LV-L8' },
};

export default function () {
  commentsIteration(BASE, vuToken(viewers), POST);
}

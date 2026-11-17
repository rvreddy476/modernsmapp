// Long video L1: watch-page open, signed-in mix.
// (long-video Track D section 6 L1, finding lv-11; related = LV-D-9 / lv-25,
//  comments top = lv-24)
//
// One iteration = one open, issued as one parallel batch like the web:
//   GET /v1/posts/:id                         post-service handler.go:116   public
//   GET /v1/videos/:id                        post-service handler.go:315   public
//   GET /v1/posts/:id/series                  post-service handler.go:185   public
//   GET /v1/videos/:id/progress               post-service handler.go:433   user (404 = no progress, expected)
//   GET /v1/channels/:ref                     post-service channels_handler.go:58  public
//   GET /v1/feed/videos/:id/related?limit=16  feed-service handler.go:101   user (20% also read page 2)
//   GET /v1/posts/:id/comments?sort=top       post-service handler.go:147   public
// Rate: ramp to 200 opens/s over 10 min, hold 20 min (about 1,450 requests/s).
// Pass: every route p95 < 300 ms and p99 < 800 ms; related p95 < 500 ms;
//   errors < 0.5%. Also required, read outside k6: post-service and
//   feed-service CPU < 70%.
//
// Data: VIEWER_TOKENS_FILE; VIDEOS_FILE (post_id, media_id, duration_ms,
//   channel_handle, author_id) of public, approved, ready long videos,
//   >= 5,000 rows drawn across the audit's length mix.

import { assertSafeTarget } from '../lib/guard.js';
import { tokens, tsv, vuToken, scale, pick, endpointThresholds } from '../lib/data.js';
import { watchOpen } from '../lib/longvideo.js';

const BASE = assertSafeTarget({ writes: false });
const viewers = tokens('VIEWER_TOKENS_FILE', 1);
const videos = tsv('VIDEOS_FILE', 100);
const PEAK = scale(200);

const route = ['p(95)<300', 'p(99)<800'];

export const options = {
  scenarios: {
    watch_open: {
      executor: 'ramping-arrival-rate',
      startRate: 1,
      timeUnit: '1s',
      preAllocatedVUs: scale(300),
      maxVUs: scale(1200),
      stages: [
        { duration: '10m', target: PEAK },
        { duration: '20m', target: PEAK },
        { duration: '1m', target: 0 },
      ],
    },
  },
  thresholds: Object.assign(
    endpointThresholds({
      post_get: route,
      video_detail: route,
      post_series: route,
      video_progress_get: route,
      channel_get: route,
      comments_top: route,
      related: ['p(95)<500', 'p(99)<800'],
    }),
    {
      watch_open_errors: ['rate<0.005'],
      http_req_failed: ['rate<0.005'],
    },
  ),
  summaryTrendStats: ['avg', 'min', 'med', 'p(95)', 'p(99)', 'max'],
  tags: { suite: 'long-video', scenario_id: 'LV-L1' },
};

export default function () {
  watchOpen(BASE, vuToken(viewers), pick(videos));
}

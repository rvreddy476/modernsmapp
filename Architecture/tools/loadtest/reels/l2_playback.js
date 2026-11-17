// Reels L2: playback start, master + child playlists and the segment-URL
// signing path.  (reels Track D section 6, reels-11; cost model 2.2)
//
// Endpoints (media-service internal/http/handler.go:172, ServeHLSPlaylist :701):
//   GET /v1/media/:mediaId/hls/master.m3u8
//   GET /v1/media/:mediaId/hls/<rung>.m3u8   (every segment URL signed here)
//   Auth: the delivery gate (no token required; post-service decides per
//   asset as the viewer via POST /v1/internal/media-access). Viewer tokens
//   are sent, as the apps do.
// Shape: Android. Each VU is one viewer swiping every SWIPE_S (4) seconds.
//   First swipe: current + 2 preloaded neighbours (3 master+child pairs);
//   later swipes: 1 new neighbour (1 pair), about 2 playlist requests/swipe.
// Rate: 3,000 concurrent viewers, about 1,500 playlist requests/s.
// Pass: /hls/ p95 < 150 ms, zero 503s. Also required, read outside k6:
//   post-service media-access p95 < 50 ms; CloudFront hit ratio > 90%
//   after warm-up (only meaningful with FETCH_SEGMENTS=1).
//
// Data: VIEWER_TOKENS_FILE; REELS_FILE (post_id, media_id, duration_ms,
//   author_id) of public, approved, HLS-ready reels, >= 1,000 rows.

import { sleep } from 'k6';
import { assertSafeTarget } from '../lib/guard.js';
import { tokens, tsv, vuToken, scale, envFloat, endpointThresholds } from '../lib/data.js';
import { playbackIteration } from '../lib/reels.js';

const BASE = assertSafeTarget({ writes: false });
const viewers = tokens('VIEWER_TOKENS_FILE', 1);
const reels = tsv('REELS_FILE', 100);
const SWIPE_S = envFloat('SWIPE_S', 4);
const VIEWERS = scale(3000);

export const options = {
  scenarios: {
    reels_playback: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '5m', target: VIEWERS },
        { duration: '20m', target: VIEWERS },
        { duration: '1m', target: 0 },
      ],
      gracefulRampDown: '30s',
    },
  },
  thresholds: Object.assign(
    endpointThresholds({
      hls_master: ['p(95)<150'],
      hls_child: ['p(95)<150'],
    }),
    {
      hls_503_total: ['count<1'],
      hls_errors: ['rate<0.005'],
    },
  ),
  summaryTrendStats: ['avg', 'min', 'med', 'p(95)', 'p(99)', 'max'],
  tags: { suite: 'reels', scenario_id: 'R-L2' },
};

export default function () {
  playbackIteration(BASE, vuToken(viewers), reels);
  sleep(SWIPE_S * (0.75 + Math.random() * 0.5));
}

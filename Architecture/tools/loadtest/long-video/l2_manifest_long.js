// Long video L2: playback start and quality switches on multi-hour videos:
// the playlist signing cost.  (long-video Track D section 6 L2; lv-12 /
// LV-D-1: one RSA signature per segment, ~2,400 for a 4 h rung)
//
// Endpoints (media-service internal/http/handler.go:172, ServeHLSPlaylist :701):
//   GET /v1/media/:mediaId/hls/master.m3u8
//   GET /v1/media/:mediaId/hls/<rung>.m3u8   every segment URL signed here
//   Auth: the delivery gate (token optional; post-service decides).
// Shape: master + one child per start; 30% of starts also fetch a second
//   child (an ABR or manual quality switch). Spread over LONG_DISTINCT (50)
//   distinct videos of 1 to 4 h, so the per-pod sign cache (20,000 entries)
//   is exercised the way the audit describes.
// Rate: 1,000 playlist requests/s (about 435 starts/s), ramp 5 min, hold 20 min.
// Pass: /hls/*.m3u8 p95 < 200 ms, zero 503s. Also required, read outside
//   k6: media-service CPU < 70%, signer calls per child playlist <= 2
//   (after the lv-12 fix), CloudFront hit ratio > 90% after warm-up.
//   hls_child_segments and hls_child_bytes in the summary show the size of
//   what was signed (a 4 h rung: ~2,400 segments, ~1.3 MB).
//
// Data: VIEWER_TOKENS_FILE; LONG_VIDEOS_FILE (same columns as VIDEOS_FILE),
//   only HLS-ready videos of 1 to 4 h.

import { assertSafeTarget } from '../lib/guard.js';
import { tokens, tsv, vuToken, scale, envInt, endpointThresholds } from '../lib/data.js';
import { fetchMaster, fetchChild, maybeFetchSegment } from '../lib/hls.js';
import { V } from '../lib/longvideo.js';

const BASE = assertSafeTarget({ writes: false });
const viewers = tokens('VIEWER_TOKENS_FILE', 1);
const all = tsv('LONG_VIDEOS_FILE', 1);
const DISTINCT = Math.min(envInt('LONG_DISTINCT', 50), all.length);
const STARTS = scale(435);

export const options = {
  scenarios: {
    long_manifest: {
      executor: 'ramping-arrival-rate',
      startRate: 1,
      timeUnit: '1s',
      preAllocatedVUs: scale(400),
      maxVUs: scale(2000),
      stages: [
        { duration: '5m', target: STARTS },
        { duration: '20m', target: STARTS },
        { duration: '1m', target: 0 },
      ],
    },
  },
  thresholds: Object.assign(
    endpointThresholds({ hls_master: ['p(95)<200'], hls_child: ['p(95)<200'] }),
    { hls_503_total: ['count<1'], hls_errors: ['rate<0.005'] },
  ),
  summaryTrendStats: ['avg', 'min', 'med', 'p(95)', 'p(99)', 'max'],
  tags: { suite: 'long-video', scenario_id: 'LV-L2' },
};

export default function () {
  const t = vuToken(viewers);
  const row = all[Math.floor(Math.random() * DISTINCT)];
  const mediaId = row[V.media];
  const children = fetchMaster(BASE, t, mediaId, { flow: 'long' });
  if (!children.length) return;
  const first = Math.floor(Math.random() * children.length);
  const segs = fetchChild(BASE, t, mediaId, children[first], { flow: 'long' });
  maybeFetchSegment(BASE, mediaId, segs);
  if (children.length > 1 && Math.random() < 0.3) {
    const second = (first + 1 + Math.floor(Math.random() * (children.length - 1))) % children.length;
    fetchChild(BASE, t, mediaId, children[second], { flow: 'long', switch: 'yes' });
  }
}

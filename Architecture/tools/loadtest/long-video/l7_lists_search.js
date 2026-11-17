// Long video L7: lists and search, plus channel pages and playlist reads.
// (long-video Track D section 6 L7; lv-26 channel search; lv-25 related is in L1)
//
// Scenario lists_search, the audit's mix at LISTS_RPS (300) requests/s:
//   40% GET /v1/feed/videos?subscribed_only=true  feed-service handler.go:96   user
//   30% GET /v1/feed/videos                        feed-service handler.go:96   user
//   10% GET /v1/channels/search?q=<1-4 chars>      post-service channels_handler.go:54  public
//   10% GET /v1/search/posts?q=&type=videos        search-service handler.go:147
//   10% GET /v1/videos/history                     post-service handler.go:430  user
// Pass: p95 < 300 ms and p99 < 800 ms; channel search p95 < 100 ms (and
//   no sequential scan in pg_stat_statements, read outside k6); errors < 0.5%.
//
// Scenario channel_playlists (asked for by the lane; no audit row, so its
//   thresholds are PROVISIONAL, borrowed from L7): CHANNEL_RPS (50)
//   iterations/s; 60% channel page (GET /v1/channels/:ref + GET
//   /v1/posts/by-author/:authorId?type=long_video), 40% playlist (GET
//   /v1/playlists/:id + /items?limit=50, 30% read page 2).
// Note: the audit names /v1/channels/search (post-service, Postgres);
//   search-service also serves GET /v1/search/channels (OpenSearch). This
//   script follows the audit; add the other if the clients move to it.
//
// Data: VIEWER_TOKENS_FILE (accounts subscribed to 20-200 channels and
//   with watch history); VIDEOS_FILE; PLAYLISTS_FILE (playlist_id; public
//   playlists, some with up to 5,000 items); SEARCH_TERMS_FILE optional
//   (one term per line; defaults to ten generic words).
// Rate: ramp 5 min, hold 20 min.

import { assertSafeTarget } from '../lib/guard.js';
import { tokens, tsv, optionalTsv, vuToken, scale, envInt, endpointThresholds } from '../lib/data.js';
import { listsIteration, channelOrPlaylistIteration } from '../lib/longvideo.js';

const BASE = assertSafeTarget({ writes: false });
const viewers = tokens('VIEWER_TOKENS_FILE', 1);
const videos = tsv('VIDEOS_FILE', 1);
const playlists = optionalTsv('PLAYLISTS_FILE');
const termsRows = optionalTsv('SEARCH_TERMS_FILE');
const terms = termsRows ? termsRows.map((r) => r[0]) : null;
const LISTS = scale(envInt('LISTS_RPS', 300));
const CHANNEL = scale(envInt('CHANNEL_RPS', 50));

function rampArrival(target, exec) {
  return {
    executor: 'ramping-arrival-rate',
    exec,
    startRate: 1,
    timeUnit: '1s',
    preAllocatedVUs: Math.max(20, target),
    maxVUs: Math.max(100, target * 4),
    stages: [
      { duration: '5m', target },
      { duration: '20m', target },
      { duration: '1m', target: 0 },
    ],
  };
}

const route = ['p(95)<300', 'p(99)<800'];

export const options = {
  scenarios: {
    lists_search: rampArrival(LISTS, 'lists'),
    channel_playlists: rampArrival(CHANNEL, 'channels'),
  },
  thresholds: Object.assign(
    endpointThresholds({
      feed_videos_subscribed: route,
      feed_videos: route,
      channels_search: ['p(95)<100', 'p(99)<800'],
      search_videos: route,
      video_history: route,
      channel_get: route,
      channel_videos: route,
      playlist_get: route,
      playlist_items: route,
    }),
    { lists_errors: ['rate<0.005'] },
  ),
  summaryTrendStats: ['avg', 'min', 'med', 'p(95)', 'p(99)', 'max'],
  tags: { suite: 'long-video', scenario_id: 'LV-L7' },
};

export function lists() {
  listsIteration(BASE, vuToken(viewers), terms);
}

export function channels() {
  channelOrPlaylistIteration(BASE, vuToken(viewers), videos, playlists);
}

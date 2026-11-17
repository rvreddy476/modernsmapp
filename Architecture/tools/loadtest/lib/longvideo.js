// Long-video (PostTube / MTube) iteration functions for ../long-video/.
//
// Routes, as registered in code (verified 17 Nov 2026):
//   post-service (gateway prefixes /v1/posts, /v1/videos, /v1/channels,
//   /v1/playlists, /v1/comments -> post-service; api-gateway main.go:480-700)
//     GET  /v1/posts/:postId                      handler.go:116      public, visibility checked
//     GET  /v1/videos/:videoId                    handler.go:315      public (owner-only fields redacted)
//     GET  /v1/posts/:postId/series               handler.go:185      public; 200 with a null view when none
//     GET  /v1/videos/:videoId/progress           handler.go:433      user; 404 when the viewer has none
//     POST /v1/videos/:videoId/progress           handler.go:432      user; {position_ms, duration_ms, completed?, sent_at_ms?}
//          :videoId is the POST id. Runs the post read gate before the upsert (lv-22).
//     GET  /v1/videos/history?limit&cursor        handler.go:430      user
//     GET  /v1/channels/:ref                      channels_handler.go:58  public
//     GET  /v1/channels/search?q&limit            channels_handler.go:54  public; q >= 1 char
//     GET  /v1/posts/by-author/:authorId?type=long_video  handler.go:102  public, viewer-scoped
//     GET  /v1/playlists/:playlistId              handler.go:389      public for public/unlisted (optionalCallerID)
//     GET  /v1/playlists/:playlistId/items?limit&cursor  handler.go:398  same rule; default 50, max 100
//     GET  /v1/posts/:postId/comments?sort=top|newest&limit&cursor  handler.go:147  public
//     GET  /v1/comments/:commentId/replies?limit&cursor  handler.go:327 (comment_routes.go)  public, oldest first
//   feed-service (user on every route)
//     GET  /v1/feed/videos[?subscribed_only=true]          handler.go:96
//     GET  /v1/feed/videos/:postId/related?limit&cursor    handler.go:101 (related.go:90)
//   search-service
//     GET  /v1/search/posts?q&type=videos                  handler.go:147 (SearchPosts)

import http from 'k6/http';
import { check } from 'k6';
import { Rate } from 'k6/metrics';
import { authHeaders, jsonHeaders, nextCursor, pick, safeJson } from './data.js';

export const watchErrors = new Rate('watch_open_errors');
export const listErrors = new Rate('lists_errors');
export const progressErrors = new Rate('progress_errors');
export const commentErrors = new Rate('comments_errors');

// VIDEOS_FILE / LONG_VIDEOS_FILE columns:
//   post_id, media_id, duration_ms, channel_handle, author_id
export const V = { post: 0, media: 1, dur: 2, handle: 3, author: 4 };

const ok200 = http.expectedStatuses(200);
const ok200or404 = http.expectedStatuses(200, 404);

function req(url, t, endpoint, name, cb) {
  return {
    method: 'GET',
    url,
    params: { headers: authHeaders(t), tags: { endpoint, name }, responseCallback: cb || ok200 },
  };
}

// ---- L1: one signed-in watch-page open (the reads the audit names; the
// web issues them in parallel, so they go out as one batch).
export function watchOpen(base, t, row) {
  const p = row[V.post];
  const handle = row[V.handle];
  const reqs = [
    req(`${base}/v1/posts/${p}`, t, 'post_get', '/v1/posts/:id'),
    req(`${base}/v1/videos/${p}`, t, 'video_detail', '/v1/videos/:id'),
    req(`${base}/v1/posts/${p}/series`, t, 'post_series', '/v1/posts/:id/series'),
    req(`${base}/v1/videos/${p}/progress`, t, 'video_progress_get', '/v1/videos/:id/progress', ok200or404),
    req(`${base}/v1/feed/videos/${p}/related?limit=16`, t, 'related', '/v1/feed/videos/:id/related'),
    req(`${base}/v1/posts/${p}/comments?sort=top&limit=20`, t, 'comments_top', '/v1/posts/:id/comments?sort=top'),
  ];
  if (handle) reqs.push(req(`${base}/v1/channels/${encodeURIComponent(handle)}`, t, 'channel_get', '/v1/channels/:ref'));
  const res = http.batch(reqs);
  let allOk = true;
  for (let i = 0; i < res.length; i++) {
    const r = res[i];
    const expected = i === 3 ? r.status === 200 || r.status === 404 : r.status === 200;
    if (!expected) allOk = false;
  }
  check(res, { 'watch open all reads ok': () => allOk });
  watchErrors.add(!allOk);

  // 20% also read related page 2.
  const relCur = res[4].status === 200 ? nextCursor(res[4]) : '';
  if (relCur && Math.random() < 0.2) {
    const r2 = http.get(`${base}/v1/feed/videos/${p}/related?limit=16&cursor=${encodeURIComponent(relCur)}`, {
      headers: authHeaders(t),
      tags: { endpoint: 'related', name: '/v1/feed/videos/:id/related', page: 'next' },
    });
    watchErrors.add(r2.status !== 200);
  }
}

// ---- L3: one progress heartbeat. sentAtMs orders reports server-side.
export function saveProgress(base, t, postId, positionMs, durationMs, tab) {
  const res = http.post(
    `${base}/v1/videos/${postId}/progress`,
    JSON.stringify({ position_ms: positionMs, duration_ms: durationMs, sent_at_ms: Date.now() }),
    { headers: jsonHeaders(t), tags: { endpoint: 'video_progress_save', name: '/v1/videos/:id/progress', tab: tab || 'first' } },
  );
  const ok = check(res, { 'progress 200': (r) => r.status === 200 || r.status === 204 });
  progressErrors.add(!ok);
  return ok;
}

// ---- L7: lists and search, the audit's mix:
//   40% subscriptions feed, 30% video feed, 10% channel search (1-4 chars),
//   10% video search, 10% watch history.
const SEARCH_WORDS = ['music', 'live', 'cooking', 'travel', 'review', 'news', 'game', 'tutorial', 'vlog', 'cricket'];
const LETTERS = 'abcdefghijklmnopqrstuvwxyz';

function randomPrefix(n) {
  let s = '';
  for (let i = 0; i < n; i++) s += LETTERS[Math.floor(Math.random() * LETTERS.length)];
  return s;
}

export function listsIteration(base, t, words) {
  const r = Math.random();
  let res;
  if (r < 0.4) {
    res = http.get(`${base}/v1/feed/videos?subscribed_only=true&limit=20`, {
      headers: authHeaders(t),
      tags: { endpoint: 'feed_videos_subscribed', name: '/v1/feed/videos?subscribed_only=true' },
    });
  } else if (r < 0.7) {
    res = http.get(`${base}/v1/feed/videos?limit=20`, {
      headers: authHeaders(t),
      tags: { endpoint: 'feed_videos', name: '/v1/feed/videos' },
    });
  } else if (r < 0.8) {
    const q = randomPrefix(1 + Math.floor(Math.random() * 4));
    res = http.get(`${base}/v1/channels/search?q=${q}&limit=20`, {
      headers: authHeaders(t),
      tags: { endpoint: 'channels_search', name: '/v1/channels/search' },
    });
  } else if (r < 0.9) {
    const q = pick(words && words.length ? words : SEARCH_WORDS);
    res = http.get(`${base}/v1/search/posts?q=${encodeURIComponent(q)}&type=videos&limit=20`, {
      headers: authHeaders(t),
      tags: { endpoint: 'search_videos', name: '/v1/search/posts?type=videos' },
    });
  } else {
    res = http.get(`${base}/v1/videos/history?limit=20`, {
      headers: authHeaders(t),
      tags: { endpoint: 'video_history', name: '/v1/videos/history' },
    });
  }
  listErrors.add(!check(res, { 'lists 200': (x) => x.status === 200 }));
}

// ---- Channel page and playlists (asked for by the lane; the audit's L7
// has no row for them): channel header + the channel's long-video tab, or a
// playlist header + its first item page.
export function channelOrPlaylistIteration(base, t, videos, playlists) {
  if (!playlists || Math.random() < 0.6) {
    const row = pick(videos);
    const res = http.batch([
      req(`${base}/v1/channels/${encodeURIComponent(row[V.handle])}`, t, 'channel_get', '/v1/channels/:ref'),
      req(`${base}/v1/posts/by-author/${row[V.author]}?type=long_video&limit=24`, t, 'channel_videos', '/v1/posts/by-author/:id?type=long_video'),
    ]);
    listErrors.add(!check(res, { 'channel page 200': (rs) => rs.every((x) => x.status === 200) }));
    return;
  }
  const pl = pick(playlists)[0];
  const res = http.batch([
    req(`${base}/v1/playlists/${pl}`, t, 'playlist_get', '/v1/playlists/:id'),
    req(`${base}/v1/playlists/${pl}/items?limit=50`, t, 'playlist_items', '/v1/playlists/:id/items'),
  ]);
  listErrors.add(!check(res, { 'playlist 200': (rs) => rs.every((x) => x.status === 200) }));
  const cur = res[1].status === 200 ? nextCursor(res[1]) : '';
  if (cur && Math.random() < 0.3) {
    const r2 = http.get(`${base}/v1/playlists/${pl}/items?limit=50&cursor=${encodeURIComponent(cur)}`, {
      headers: authHeaders(t),
      tags: { endpoint: 'playlist_items', name: '/v1/playlists/:id/items', page: 'next' },
    });
    listErrors.add(r2.status !== 200);
  }
}

// ---- L8: comments on the 50,000-comment video. Each VU walks pages 1..10
// of one sort order, one page per iteration, and opens the replies of a
// comment that has some on 30% of pages.
let cState = null;
export function commentsIteration(base, t, postId) {
  if (!cState || cState.page >= cState.depth || (cState.page > 0 && !cState.cursor)) {
    cState = { sort: Math.random() < 0.5 ? 'top' : 'newest', page: 0, depth: 1 + Math.floor(Math.random() * 10), cursor: '' };
  }
  const url =
    `${base}/v1/posts/${postId}/comments?sort=${cState.sort}&limit=20` +
    (cState.cursor ? `&cursor=${encodeURIComponent(cState.cursor)}` : '');
  const res = http.get(url, {
    headers: authHeaders(t),
    tags: { endpoint: `comments_${cState.sort}`, name: `/v1/posts/:id/comments?sort=${cState.sort}`, page: String(cState.page + 1) },
  });
  const ok = check(res, { 'comments 200': (x) => x.status === 200 });
  commentErrors.add(!ok);
  cState.page += 1;
  cState.cursor = ok ? nextCursor(res) : '';
  if (!ok || Math.random() >= 0.3) return;
  const b = safeJson(res);
  const withReplies = b && Array.isArray(b.data) ? b.data.filter((c) => c.reply_count > 0) : [];
  if (!withReplies.length) return;
  const c = pick(withReplies);
  const rr = http.get(`${base}/v1/comments/${c.id}/replies?limit=20`, {
    headers: authHeaders(t),
    tags: { endpoint: 'comment_replies', name: '/v1/comments/:id/replies' },
  });
  commentErrors.add(!check(rr, { 'replies 200': (x) => x.status === 200 }));
}

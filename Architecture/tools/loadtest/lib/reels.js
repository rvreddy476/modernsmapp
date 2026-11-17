// Reels iteration functions. The scripts in ../reels/ wire them to k6
// scenarios; the soak script reuses them at a lower rate.
//
// Routes, as registered in code (verified 17 Nov 2026; paths through the
// api-gateway prefix table, api-gateway/cmd/server/main.go:480-700):
//   GET  /v1/feed/reels?limit&cursor&following_only&category
//        feed-service internal/http/handler.go:94 (GetReelFeed :261). user
//        (401 without X-User-Id). limit default 20, max 50. next page:
//        meta.next_cursor.
//   GET  /v1/posts/categories?kind=short   post-service handler.go:92. public
//   POST /v1/posts/:postId/like            post-service handler.go:144
//        (ToggleLike, reactions_handler.go:116). user. A TOGGLE: two calls
//        like then unlike.
//   POST /v1/posts/:postId/comments {text} post-service handler.go:146
//        (AddComment). user. Idempotency-Key honoured; per-user comment
//        rate limit answers 429 RATE_LIMITED.
//   POST /v1/graph/follow {user_id}        graph-service internal/http/handler.go:115.
//   POST /v1/graph/unfollow {user_id}      handler.go:116. user. The gateway
//        stamps the graph write-source header (pkg/edgeheaders).
//   GET  /v1/posts/trending?content_type=flick   post-service handler.go:284. public
//   GET  /v1/reels/hashtags/trending?limit&days  post-service reel_discovery.go:15. public
//   GET  /v1/discover/trending             search-service internal/http/handler.go:187.
//        NOTE: /v1/discover is its own gateway prefix (main.go:668); the
//        reels map lists it as /v1/search/discover/trending, which does not exist.

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Counter } from 'k6/metrics';
import exec from 'k6/execution';
import { authHeaders, jsonHeaders, nextCursor, pick, safeJson, uuidv4 } from './data.js';
import { startPlayback, fetchMaster, fetchChild, maybeFetchSegment } from './hls.js';
import { newSession, impression, playStart, heartbeat, playEnd, postEvents } from './analytics.js';

export const feedErrors = new Rate('feed_errors');
export const engagementErrors = new Rate('engagement_errors');
export const engagement429 = new Counter('engagement_429_total');
export const trendingErrors = new Rate('trending_errors');

// REELS_FILE columns: post_id, media_id, duration_ms, author_id
export const COL = { post: 0, media: 1, dur: 2, author: 3 };

export function loadCategories(base) {
  const fromEnv = String(__ENV.REEL_CATEGORIES || '').trim();
  if (fromEnv) return fromEnv.split(',').map((s) => s.trim()).filter(Boolean);
  const res = http.get(`${base}/v1/posts/categories?kind=short`, { tags: { endpoint: 'categories', name: '/v1/posts/categories' } });
  const b = safeJson(res);
  const list = b && Array.isArray(b.data) ? b.data.map((c) => c.slug || c.id).filter(Boolean) : [];
  return list;
}

// ---- L1: reels feed pages (70% plain, 20% following_only, 10% category)
export function feedIteration(base, t, cats) {
  const r = Math.random();
  let q = 'limit=15';
  let mode = 'plain';
  if (r < 0.2) {
    q += '&following_only=true';
    mode = 'following';
  } else if (r < 0.3 && cats && cats.length) {
    q += `&category=${encodeURIComponent(pick(cats))}`;
    mode = 'category';
  }
  const first = http.get(`${base}/v1/feed/reels?${q}`, {
    headers: authHeaders(t),
    tags: { endpoint: 'feed_reels', name: '/v1/feed/reels', page: 'first', mode },
  });
  const ok1 = check(first, {
    'feed_reels 200': (x) => x.status === 200,
    'feed_reels data array': (x) => {
      const b = safeJson(x);
      return !!b && Array.isArray(b.data);
    },
  });
  feedErrors.add(!ok1);
  const cur = ok1 ? nextCursor(first) : '';
  if (cur && Math.random() < 0.5) {
    sleep(2 + Math.random() * 4);
    const next = http.get(`${base}/v1/feed/reels?${q}&cursor=${encodeURIComponent(cur)}`, {
      headers: authHeaders(t),
      tags: { endpoint: 'feed_reels', name: '/v1/feed/reels', page: 'next', mode },
    });
    feedErrors.add(!check(next, { 'feed_reels next 200': (x) => x.status === 200 }));
  }
}

// ---- L2: playback start, Android shape. Each VU is a viewer swiping every
// SWIPE_S seconds. The first swipe opens the current reel plus 2 preloaded
// neighbours (3 master+child pairs); every later swipe preloads 1 new
// neighbour (1 pair), which is the steady state of current +/- 1 preload
// (reels Track D 2.2): about 2 playlist requests per swipe.
let playIdx = -1;
export function playbackIteration(base, t, reels) {
  if (playIdx < 0) {
    playIdx = Math.floor(Math.random() * reels.length);
    for (let k = 0; k < 3; k++) {
      startPlayback(base, t, reels[(playIdx + k) % reels.length][COL.media], { tags: { flow: 'reels' } });
    }
    playIdx += 3;
  } else {
    startPlayback(base, t, reels[playIdx % reels.length][COL.media], { tags: { flow: 'reels' } });
    playIdx += 1;
  }
}

// ---- L3: analytics. Web shape: one event per request, a heartbeat every
// 5 s per viewer. Android shape: events buffered and flushed as one batch
// (50 to 100 events) every ANDROID_FLUSH_S seconds.
let session = null;
let buffer = [];
let lastFlush = 0;
export function analyticsWebTick(base, t, reels, surface) {
  if (!session || session.watched >= session.durationMs) {
    if (session) postEvents(base, t, [playEnd(session, 'swipe_next')], 'web');
    const row = pick(reels);
    session = newSession(row[COL.post], parseInt(row[COL.dur], 10) || 30000, surface || 'reels');
    postEvents(base, t, [impression(session)], 'web');
    postEvents(base, t, [playStart(session)], 'web');
    return;
  }
  postEvents(base, t, [heartbeat(session, 5000)], 'web');
}

// analyticsBufferedTick is one 5-second tick of a viewer whose client
// buffers events and flushes them every flushS seconds (Android: batches of
// 50 to 100; the long-video web player: every 10 s). swipeProb is the
// chance per tick of moving to the next item (0.25 for reels, 0 for long
// video, where a session ends only at the end of the video).
export function analyticsBufferedTick(base, t, reels, surface, flushS, swipeProb, shape) {
  if (!session || session.watched >= session.durationMs || Math.random() < swipeProb) {
    if (session && session.started) buffer.push(playEnd(session, 'swipe_next'));
    const row = pick(reels);
    session = newSession(row[COL.post], parseInt(row[COL.dur], 10) || 30000, surface || 'reels');
    buffer.push(impression(session), playStart(session));
  }
  buffer.push(heartbeat(session, 5000));
  const now = Date.now();
  if (!lastFlush) lastFlush = now - Math.random() * flushS * 1000; // spread first flushes
  if (now - lastFlush >= flushS * 1000 || buffer.length >= 100) {
    const batch = buffer.splice(0, 200);
    postEvents(base, t, batch, shape || 'android');
    lastFlush = now;
  }
}

// ---- Engagement writes: 60% like toggle, 25% comment, 15% follow/unfollow.
let followed = {};
export function engagementIteration(base, t, reels) {
  const row = pick(reels);
  const r = Math.random();
  let res;
  if (r < 0.6) {
    res = http.post(`${base}/v1/posts/${row[COL.post]}/like`, null, {
      headers: authHeaders(t),
      tags: { endpoint: 'post_like', name: '/v1/posts/:id/like' },
    });
  } else if (r < 0.85) {
    res = http.post(
      `${base}/v1/posts/${row[COL.post]}/comments`,
      JSON.stringify({ text: `loadtest comment ${exec.vu.idInTest}-${exec.vu.iterationInScenario}` }),
      {
        headers: jsonHeaders(t, { 'Idempotency-Key': uuidv4() }),
        tags: { endpoint: 'post_comment', name: '/v1/posts/:id/comments' },
      },
    );
  } else {
    const target = row[COL.author];
    if (!target) return;
    const action = followed[target] ? 'unfollow' : 'follow';
    res = http.post(`${base}/v1/graph/${action}`, JSON.stringify({ user_id: target }), {
      headers: jsonHeaders(t),
      tags: { endpoint: `graph_${action}`, name: `/v1/graph/${action}` },
    });
    if (res.status === 200) followed[target] = action === 'follow';
  }
  if (res.status === 429) engagement429.add(1);
  // A follow of yourself is a 400 when the viewer happens to be the author.
  engagementErrors.add(!check(res, { 'engagement 2xx': (x) => x.status >= 200 && x.status < 300 }));
}

// ---- Trending and discovery: 40% trending reels, 30% trending hashtags,
// 30% search-service discover/trending.
export function trendingIteration(base, t) {
  const r = Math.random();
  let res;
  if (r < 0.4) {
    res = http.get(`${base}/v1/posts/trending?content_type=flick&limit=20`, {
      headers: authHeaders(t),
      tags: { endpoint: 'posts_trending', name: '/v1/posts/trending' },
    });
    const cur = res.status === 200 ? nextCursor(res) : '';
    if (cur && Math.random() < 0.3) {
      const p2 = http.get(`${base}/v1/posts/trending?content_type=flick&limit=20&cursor=${encodeURIComponent(cur)}`, {
        headers: authHeaders(t),
        tags: { endpoint: 'posts_trending', name: '/v1/posts/trending', page: 'next' },
      });
      trendingErrors.add(p2.status !== 200);
    }
  } else if (r < 0.7) {
    res = http.get(`${base}/v1/reels/hashtags/trending?limit=20&days=7`, {
      headers: authHeaders(t),
      tags: { endpoint: 'reels_hashtags_trending', name: '/v1/reels/hashtags/trending' },
    });
  } else {
    res = http.get(`${base}/v1/discover/trending`, {
      headers: authHeaders(t),
      tags: { endpoint: 'discover_trending', name: '/v1/discover/trending' },
    });
  }
  trendingErrors.add(!check(res, { 'trending 200': (x) => x.status === 200 }));
}

export { fetchMaster, fetchChild, maybeFetchSegment };

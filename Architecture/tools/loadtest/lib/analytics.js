// Client analytics ingest: POST /v1/analytics/events
// (analytics-service internal/http/handler.go:93 IngestEvents :166;
// service/ingest.go). Auth: user (the gateway actor, X-User-Id). Body:
//   {"events": [{event_id (16-128 chars), type, timestamp (RFC 3339,
//     within 24 h back / 5 min ahead), payload: {content_id (the POST id,
//     which must already be projected or the batch is a 422
//     CONTENT_NOT_READY), session_id, surface, ...}}]}
// Batch 1..200. One invalid event rejects the whole batch (reels Track C),
// so the generator only builds events the validator accepts.
// 202 = durable.

import http from 'k6/http';
import { check } from 'k6';
import { Rate, Counter } from 'k6/metrics';
import { jsonHeaders, uuidv4 } from './data.js';

export const analyticsErrors = new Rate('analytics_errors');
export const analyticsEvents = new Counter('analytics_events_sent');
export const analyticsNotReady = new Counter('analytics_content_not_ready');

function ev(type, payload) {
  return { event_id: uuidv4(), type, timestamp: new Date().toISOString(), payload };
}

// Session is one viewer watching one item: impression, play_start, n
// heartbeats (5 s apart in media time), play_end.
export function newSession(contentId, durationMs, surface) {
  return { contentId, durationMs: Math.max(1000, durationMs | 0), surface, sessionId: uuidv4(), watched: 0, started: false };
}

export function impression(s) {
  return ev('impression', { content_id: s.contentId, surface: s.surface, visible_ms: 1200, is_autoplay: true });
}

export function playStart(s) {
  s.started = true;
  return ev('play_start', {
    content_id: s.contentId,
    session_id: s.sessionId,
    surface: s.surface,
    content_duration_ms: s.durationMs,
    start_method: 'autoplay',
    is_autoplay: true,
    time_to_first_frame_ms: 400 + Math.floor(Math.random() * 600),
    initial_buffer_ms: 200,
  });
}

export function heartbeat(s, incMs) {
  // The server clamps a running total past duration x (loops + 1); it
  // never rejects it, so a long-running viewer stays valid.
  const inc = incMs;
  s.watched += inc;
  return ev('watch_heartbeat', {
    content_id: s.contentId,
    session_id: s.sessionId,
    surface: s.surface,
    watched_ms_increment: inc,
    watched_ms_total: s.watched,
    playhead_position_ms: s.watched % s.durationMs,
    buffering_ms_increment: 0,
    seek_count_increment: 0,
    playback_speed: 1,
  });
}

export function playEnd(s, reason) {
  return ev('play_end', {
    content_id: s.contentId,
    session_id: s.sessionId,
    surface: s.surface,
    content_duration_ms: s.durationMs,
    watched_ms_total: s.watched,
    max_continuous_watch_ms: s.watched,
    loop_count: 0,
    end_reason: reason || 'swipe_next',
  });
}

export function postEvents(base, t, events, shape) {
  const res = http.post(`${base}/v1/analytics/events`, JSON.stringify({ events }), {
    headers: jsonHeaders(t),
    tags: { endpoint: 'analytics_events', name: '/v1/analytics/events', shape },
  });
  if (res.status === 422) analyticsNotReady.add(1);
  const ok = check(res, { 'analytics 202': (r) => r.status === 202 });
  analyticsErrors.add(!ok);
  analyticsEvents.add(events.length);
  return ok;
}

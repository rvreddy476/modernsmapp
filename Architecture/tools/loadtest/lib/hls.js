// HLS playlist fetches through the gateway:
//   GET /v1/media/:mediaId/hls/master.m3u8
//   GET /v1/media/:mediaId/hls/<rung>.m3u8
// (media-service internal/http/handler.go:172, ServeHLSPlaylist :701;
// service/playback.go:247-324). Auth is the delivery "gate": no token is
// required, post-service decides per asset as the viewer. The scripts send
// the viewer's token, as the signed-in apps do.
//
// The child playlist is where every segment URL is signed
// (delivery/authz.go HLSURLsForAsset, sign_cache.go), so its latency is the
// segment-URL signing cost the audits ask to measure (reels D-7, lv-12).

import http from 'k6/http';
import { check } from 'k6';
import { Trend, Counter, Rate } from 'k6/metrics';
import { authHeaders } from './data.js';

export const hlsSegmentsSigned = new Trend('hls_child_segments', false);
export const hlsChildBytes = new Trend('hls_child_bytes', false);
export const hls503 = new Counter('hls_503_total');
export const hlsErrors = new Rate('hls_errors');
export const segmentErrors = new Rate('hls_segment_errors');

// childURIs returns the variant playlist URIs listed in a master playlist.
export function childURIs(masterBody) {
  const out = [];
  const lines = String(masterBody || '').split('\n');
  for (let i = 0; i < lines.length; i++) {
    const l = lines[i].trim();
    if (!l || l.startsWith('#')) continue;
    if (/\.m3u8(\?|$)/.test(l)) out.push(l);
  }
  return out;
}

export function resolveURI(base, mediaId, uri) {
  if (/^https?:\/\//i.test(uri)) return uri;
  if (uri.startsWith('/')) return `${base}${uri}`;
  return `${base}/v1/media/${mediaId}/hls/${uri}`;
}

function segmentLines(body) {
  return String(body || '')
    .split('\n')
    .map((l) => l.trim())
    .filter((l) => l && !l.startsWith('#'));
}

function record(res, tagEndpoint) {
  if (res.status === 503) hls503.add(1);
  const ok = check(res, {
    [`${tagEndpoint} 200`]: (r) => r.status === 200,
    [`${tagEndpoint} is a playlist`]: (r) => String(r.body || '').startsWith('#EXTM3U'),
  });
  hlsErrors.add(!ok);
  return ok;
}

// fetchMaster fetches the master playlist and returns the child URIs.
export function fetchMaster(base, t, mediaId, tags) {
  const res = http.get(`${base}/v1/media/${mediaId}/hls/master.m3u8`, {
    headers: authHeaders(t, { Accept: 'application/vnd.apple.mpegurl' }),
    tags: Object.assign({ endpoint: 'hls_master', name: '/v1/media/:id/hls/master.m3u8' }, tags || {}),
  });
  if (!record(res, 'hls_master')) return [];
  return childURIs(res.body);
}

// fetchChild fetches one variant playlist; returns its segment URLs.
export function fetchChild(base, t, mediaId, uri, tags) {
  const res = http.get(resolveURI(base, mediaId, uri), {
    headers: authHeaders(t, { Accept: 'application/vnd.apple.mpegurl' }),
    tags: Object.assign({ endpoint: 'hls_child', name: '/v1/media/:id/hls/:rung.m3u8' }, tags || {}),
  });
  if (!record(res, 'hls_child')) return [];
  const segs = segmentLines(res.body);
  hlsSegmentsSigned.add(segs.length);
  hlsChildBytes.add(String(res.body || '').length);
  return segs;
}

// maybeFetchSegment fetches the first segment from the CDN when
// FETCH_SEGMENTS=1. Off by default: CDN bytes cost money and the audits'
// CloudFront hit ratio comes from CloudFront metrics, not from k6. A
// signed URL is fetched with no bearer, as the players do.
export function maybeFetchSegment(base, mediaId, segs) {
  if (String(__ENV.FETCH_SEGMENTS || '') !== '1' || !segs.length) return;
  const url = resolveURI(base, mediaId, segs[0]);
  const res = http.get(url, {
    responseType: 'none',
    tags: { endpoint: 'hls_segment', name: 'segment (CDN)' },
  });
  segmentErrors.add(res.status !== 200);
}

// startPlayback is one viewer starting one video: master, then a child.
// childIndex picks a rung (default: the first listed, which is what a
// player starts on before ABR settles). Returns the child URIs so a
// caller can simulate a quality switch.
export function startPlayback(base, t, mediaId, opts) {
  const o = opts || {};
  const children = fetchMaster(base, t, mediaId, o.tags);
  if (!children.length) return [];
  const idx = Math.min(o.childIndex || 0, children.length - 1);
  const segs = fetchChild(base, t, mediaId, children[idx], o.tags);
  maybeFetchSegment(base, mediaId, segs);
  return children;
}

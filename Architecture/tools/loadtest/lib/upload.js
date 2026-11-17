// Upload and transcode helpers (media-service).
//
// Simple path (internal/http/handler.go:134-135, service/upload.go):
//   POST /v1/media/init     {file_type, mime_type, file_size_bytes}  user
//        -> data {media_id, upload_url, upload_headers, ...}
//   PUT  <upload_url>       straight to the object store, with upload_headers
//   POST /v1/media/confirm  {media_id}                                user
// Resumable path (internal/http/resumable_handler.go:19-22):
//   POST /v1/media/upload/resumable/init   {file_type, mime_type, total_bytes}
//        -> data {upload_id, media_id, chunk_size, total_parts}
//   POST /v1/media/upload/resumable/:uploadId/chunk?part_number=N  raw bytes
//   POST /v1/media/upload/resumable/:uploadId/complete
// Status (handler.go:161, GetMediaStatus :562; the uploader always sees it):
//   GET  /v1/media/:mediaId/status
//        -> data {processing_status, transcoding_jobs[{status,...}], ...}
//
// Both init routes count against the per-user upload budget: 10 per rolling
// hour and 30 per rolling day (service/ratelimit.go). A burst of N uploads
// therefore needs at least N/10 distinct uploader accounts; the scripts use
// one account per upload where the token file allows it.
//
// The bytes come from a real video file (SAMPLE_VIDEO_FILE or
// LONG_SAMPLE_FILE): confirm runs a magic-byte check and the worker runs
// ffprobe, so random bytes would be rejected and measure nothing. Files are
// read in pieces through k6/experimental/fs (k6 >= 0.52), so a 1 GB sample
// is never held whole in every VU.

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Trend, Counter, Rate } from 'k6/metrics';
import { open as fsOpen, SeekMode } from 'k6/experimental/fs';
import { requireEnv, jsonHeaders, authHeaders, safeJson, envInt } from './data.js';

export const uploadErrors = new Rate('upload_errors');
export const partErrors = new Rate('upload_part_errors');
export const queueAge = new Trend('transcode_queue_age_ms', true);
export const confirmToReady = new Trend('transcode_confirm_to_ready_ms', true);
export const transcodeFailed = new Counter('transcode_failed_total');
export const transcodeStuck = new Counter('transcode_stuck_total');
export const transcodeReady = new Counter('transcode_ready_total');

// sampleFile opens a file for chunked reads. Call it at the top level of a
// script (init context). The returned holder fills in once the open
// resolves, before any VU iteration starts.
export function sampleFile(envName, mimeEnvName) {
  const holder = {
    path: requireEnv(envName),
    mime: String(__ENV[mimeEnvName || 'SAMPLE_MIME'] || 'video/mp4'),
    file: null,
    size: 0,
  };
  (async function () {
    holder.file = await fsOpen(holder.path);
    const st = await holder.file.stat();
    holder.size = st.size;
  })();
  return holder;
}

// syntheticFile stands in for a sample file with zero bytes of the given
// size. It exercises init, every part and complete, but confirm's magic
// check refuses the content, so complete answers 400 and nothing is
// transcoded: use it only for the upload-path latency scenario.
export function syntheticFile(sizeBytes) {
  return { path: '(synthetic)', mime: 'video/mp4', file: null, size: sizeBytes, synthetic: true };
}

export async function readRange(holder, offset, length) {
  if (holder.synthetic) {
    return new Uint8Array(length).buffer;
  }
  await holder.file.seek(offset, SeekMode.Start);
  const out = new Uint8Array(length);
  let got = 0;
  while (got < length) {
    const tmp = new Uint8Array(Math.min(length - got, 8 * 1024 * 1024));
    const n = await holder.file.read(tmp);
    if (n === null || n === 0) break;
    out.set(tmp.subarray(0, n), got);
    got += n;
  }
  return got === length ? out.buffer : out.buffer.slice(0, got);
}

function stripLength(h) {
  const out = {};
  for (const [k, v] of Object.entries(h || {})) {
    if (k.toLowerCase() !== 'content-length') out[k] = v;
  }
  return out;
}

// simpleUpload runs init + PUT + confirm. Returns {mediaId, confirmedAt}
// or null on failure.
export async function simpleUpload(base, t, holder, tags) {
  const tg = (endpoint, name) => Object.assign({ endpoint, name }, tags || {});
  const init = http.post(
    `${base}/v1/media/init`,
    JSON.stringify({ file_type: 'video', mime_type: holder.mime, file_size_bytes: holder.size }),
    { headers: jsonHeaders(t), tags: tg('media_init', '/v1/media/init') },
  );
  const initBody = safeJson(init);
  const okInit = check(init, { 'media_init 200': (r) => r.status === 200 });
  if (!okInit || !initBody || !initBody.data) {
    uploadErrors.add(1);
    if (init.status === 429) console.warn('media_init 429: upload budget hit; add uploader accounts');
    return null;
  }
  const d = initBody.data;

  const bytes = await readRange(holder, 0, holder.size);
  const put = http.put(d.upload_url, bytes, {
    headers: stripLength(d.upload_headers),
    responseType: 'none',
    tags: tg('object_put', 'PUT presigned upload_url'),
  });
  if (!check(put, { 'object_put 2xx': (r) => r.status >= 200 && r.status < 300 })) {
    uploadErrors.add(1);
    return null;
  }

  const confirm = http.post(`${base}/v1/media/confirm`, JSON.stringify({ media_id: d.media_id }), {
    headers: jsonHeaders(t),
    tags: tg('media_confirm', '/v1/media/confirm'),
  });
  const okConfirm = check(confirm, { 'media_confirm 200': (r) => r.status === 200 });
  uploadErrors.add(!okConfirm);
  return okConfirm ? { mediaId: d.media_id, confirmedAt: Date.now() } : null;
}

// resumableUpload runs init + every part + complete, in the server's chunk
// size. Returns {mediaId, confirmedAt} or null.
export async function resumableUpload(base, t, holder, tags) {
  const tg = (endpoint, name) => Object.assign({ endpoint, name }, tags || {});
  const init = http.post(
    `${base}/v1/media/upload/resumable/init`,
    JSON.stringify({ file_type: 'video', mime_type: holder.mime, total_bytes: holder.size }),
    { headers: jsonHeaders(t), tags: tg('resumable_init', '/v1/media/upload/resumable/init') },
  );
  const body = safeJson(init);
  if (!check(init, { 'resumable_init 200': (r) => r.status === 200 }) || !body || !body.data) {
    uploadErrors.add(1);
    return null;
  }
  const { upload_id: uploadId, media_id: mediaId, chunk_size: chunk, total_parts: parts } = body.data;

  for (let p = 1; p <= parts; p++) {
    const offset = (p - 1) * chunk;
    const len = Math.min(chunk, holder.size - offset);
    const bytes = await readRange(holder, offset, len);
    const res = http.post(`${base}/v1/media/upload/resumable/${uploadId}/chunk?part_number=${p}`, bytes, {
      headers: authHeaders(t, { 'Content-Type': 'application/octet-stream' }),
      tags: tg('resumable_chunk', '/v1/media/upload/resumable/:id/chunk'),
      timeout: '300s',
    });
    const ok = res.status === 200;
    partErrors.add(!ok);
    if (!ok) {
      // One retry, as the clients do, before giving up on the upload.
      const again = http.post(`${base}/v1/media/upload/resumable/${uploadId}/chunk?part_number=${p}`, bytes, {
        headers: authHeaders(t, { 'Content-Type': 'application/octet-stream' }),
        tags: tg('resumable_chunk', '/v1/media/upload/resumable/:id/chunk'),
        timeout: '300s',
      });
      partErrors.add(again.status !== 200);
      if (again.status !== 200) {
        uploadErrors.add(1);
        return null;
      }
    }
  }

  const done = http.post(`${base}/v1/media/upload/resumable/${uploadId}/complete`, null, {
    headers: authHeaders(t),
    tags: tg('resumable_complete', '/v1/media/upload/resumable/:id/complete'),
    timeout: '300s',
    // Synthetic bytes are refused at confirm by design (400); that is the
    // expected answer, not a failed request.
    responseCallback: holder.synthetic ? http.expectedStatuses(200, 400) : http.expectedStatuses({ min: 200, max: 299 }),
  });
  if (holder.synthetic) {
    const ok = check(done, { 'resumable_complete answered (synthetic)': (r) => r.status === 200 || r.status === 400 });
    uploadErrors.add(!ok);
    return null;
  }
  const ok = check(done, { 'resumable_complete 200': (r) => r.status === 200 });
  uploadErrors.add(!ok);
  return ok ? { mediaId, confirmedAt: Date.now() } : null;
}

// waitForTranscode polls the status route as the uploader until the asset
// is ready or failed, or until maxWaitS passes (then it counts as stuck:
// the audits' "no item in processing past 15 min" for reels).
// queue age = confirm until the worker has started a rendition job.
export function waitForTranscode(base, t, up, opts) {
  const o = opts || {};
  const pollS = o.pollS || envInt('POLL_INTERVAL_S', 5);
  const maxWaitS = o.maxWaitS || envInt('MAX_WAIT_S', 900);
  const tags = Object.assign({ endpoint: 'media_status', name: '/v1/media/:id/status' }, o.tags || {});
  const metricTags = o.metricTags || {};
  let started = 0;
  const deadline = up.confirmedAt + maxWaitS * 1000;
  while (Date.now() < deadline) {
    sleep(pollS);
    const res = http.get(`${base}/v1/media/${up.mediaId}/status`, { headers: authHeaders(t), tags });
    const b = safeJson(res);
    if (res.status !== 200 || !b || !b.data) continue;
    const s = b.data;
    const jobs = s.transcoding_jobs || [];
    if (!started && jobs.some((j) => j.status && j.status !== 'queued')) {
      started = Date.now();
      queueAge.add(started - up.confirmedAt, metricTags);
    }
    if (s.processing_status === 'ready') {
      if (!started) queueAge.add(Date.now() - up.confirmedAt, metricTags);
      confirmToReady.add(Date.now() - up.confirmedAt, metricTags);
      transcodeReady.add(1, metricTags);
      timeline(up, started, 'ready', metricTags);
      return 'ready';
    }
    if (s.processing_status === 'failed' || s.processing_status === 'rejected') {
      transcodeFailed.add(1, metricTags);
      timeline(up, started, s.processing_status, metricTags);
      return s.processing_status;
    }
  }
  transcodeStuck.add(1, metricTags);
  timeline(up, started, 'stuck', metricTags);
  return 'stuck';
}

// timeline prints one line per job when LOG_TIMELINE=1, so lane occupancy
// (how many jobs ran at once) can be rebuilt after the run. See README.
function timeline(up, started, outcome, metricTags) {
  if (String(__ENV.LOG_TIMELINE || '') !== '1') return;
  console.log(
    'TIMELINE ' +
      JSON.stringify({
        media_id: up.mediaId,
        class: metricTags.class || '',
        confirmed_at: up.confirmedAt,
        started_at: started || null,
        finished_at: Date.now(),
        outcome,
      }),
  );
}

#!/usr/bin/env node
// Post-deploy dev smoke test. Dependency-free: Node 20+ built-ins only.
//
//   bash scripts/smoke/dev-smoke.sh [--api <url>] [--only a,b] [--skip a,b]
//                                   [--keep] [--video <file>] [--follow-user <name>]
//
// Output contract (same shape as scripts/verify):
//   PASS <step> (<ms>ms) [note]
//   FAIL <step> (<ms>ms) <short reason>
//   SKIP <step> (needs <dep>)
//   RESULT PASS | RESULT FAIL (<n> failed)
// Exit 0 on pass, 1 on any failure, 2 on a refused target or a usage error.
//
// Safety rules enforced here, not by convention:
//   - only a dev host (a `dev` label, e.g. api-dev.*, *.dev.*) or localhost;
//     anything else is refused before a single request is made;
//   - one login per run (the login limiter is 10 per IP per 15 min);
//   - nothing secret is ever printed: response bodies are redacted by key
//     name and by pattern, scrubbed of the run's own credentials, and cut
//     to ~300 characters;
//   - the token is only ever sent to the API origin, never to a storage URL;
//   - cleanup deletes only what this run created, and runs after failures.
//   - the shop steps stop at a price quote: no checkout, no payment intent.
//
// Routes are documented in docs/harness/DEV-SMOKE.md.

import { readFileSync, existsSync, statSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

export const DEFAULT_API = 'https://api-dev.cleestudio.com';
export const DEFAULT_ACCOUNT_FILE = 'C:\\workspace\\modernsmapp\\.secrets\\harness-test-account.env';
export const DEFAULT_FOLLOW_USER = 'call_b';
export const BODY_LIMIT = 300;

const SCRIPT_DIR = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(SCRIPT_DIR, '..', '..');

// The tracked 2 s fixture first (13.5 KB, a Short), then local clips in the
// gitignored .tmp/ folder. Override with --video or DEV_SMOKE_VIDEO.
export const VIDEO_CANDIDATES = [
  'scripts/smoke/fixtures/smoke-2s-portrait.mp4',
  '.tmp/android-contract-portrait.mp4',
  '.tmp/android-contract-1s.mp4',
  '.tmp/android-contract-5s.mp4',
];

// ── Pure helpers (unit-tested in dev-smoke.test.mjs) ─────────────────────

const LOCAL_HOSTS = new Set(['localhost', '127.0.0.1', '::1', '[::1]']);
const NON_DEV_WORDS = /(^|[-.])(prod|production|staging|stage|stg|preprod|live)([-.]|$)/i;

/** True for localhost or a hostname with a `dev` label; never for prod/staging. */
export function hostIsDev(hostname) {
  if (typeof hostname !== 'string' || hostname === '') return false;
  const host = hostname.toLowerCase().replace(/\.$/, '');
  if (LOCAL_HOSTS.has(host) || host.endsWith('.localhost')) return true;
  if (NON_DEV_WORDS.test(host)) return false;
  const labels = host.split('.');
  if (labels.length < 2) return false;
  return labels.some((l) => l === 'dev' || l.startsWith('dev-') || l.endsWith('-dev'));
}

/**
 * The storage host an upload or playback URL points at. The dev API hands out
 * presigned S3 URLs on its own bucket (atpost-devbox-media-<acct>.s3...), whose
 * name carries "devbox" rather than a bare "dev" label, so this accepts a
 * dev/devbox token anywhere in the name. Prod and staging words still refuse.
 * Only the API host check (hostIsDev) gates where the smoke runs at all.
 */
export function storageHostIsDev(hostname) {
  if (hostIsDev(hostname)) return true;
  if (typeof hostname !== 'string' || hostname === '') return false;
  const host = hostname.toLowerCase();
  if (NON_DEV_WORDS.test(host)) return false;
  return host.split(/[-.]/).some((t) => t === 'dev' || t === 'devbox');
}

/** Validates the --api value. Returns {ok, origin, host} or {ok:false, host, reason}. */
export function checkTarget(raw) {
  let url;
  try {
    url = new URL(raw);
  } catch {
    return { ok: false, host: String(raw), reason: 'not a URL' };
  }
  const host = url.hostname;
  if (url.username || url.password) return { ok: false, host, reason: 'credentials in URL' };
  if (!hostIsDev(host)) return { ok: false, host, reason: 'not a dev host' };
  const local = LOCAL_HOSTS.has(host) || host.endsWith('.localhost');
  if (url.protocol !== 'https:' && !(local && url.protocol === 'http:')) {
    return { ok: false, host, reason: 'https required off localhost' };
  }
  return { ok: true, origin: url.origin, host };
}

/** Minimal .env parser: KEY=VALUE lines, # comments, optional `export`, quotes. */
export function parseEnv(text) {
  const out = {};
  for (const rawLine of String(text).replace(/^\uFEFF/, '').split(/\r?\n/)) {
    const line = rawLine.trim();
    if (line === '' || line.startsWith('#')) continue;
    const m = line.match(/^(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$/);
    if (!m) continue;
    let value = m[2];
    if (value.length >= 2 && (value[0] === '"' || value[0] === "'") && value.endsWith(value[0])) {
      value = value.slice(1, -1);
    }
    // Unquoted values are taken whole: a password may contain '#' or '='.
    out[m[1]] = value;
  }
  return out;
}

const SENSITIVE_KEY = /(token|password|passwd|secret|authori[sz]ation|cookie|signature|credential|api[-_]?key|otp|session|email|phone|pending)/i;

/** Deep copy with every sensitive-looking field replaced. */
export function redact(value, depth = 0) {
  if (depth > 20) return '[DEPTH]';
  if (Array.isArray(value)) return value.map((v) => redact(v, depth + 1));
  if (value && typeof value === 'object') {
    const out = {};
    for (const [k, v] of Object.entries(value)) {
      out[k] = SENSITIVE_KEY.test(k) ? '[REDACTED]' : redact(v, depth + 1);
    }
    return out;
  }
  if (typeof value === 'string') return scrubPatterns(value);
  return value;
}

/** Pattern-level scrub for free text (JWTs, bearer values, signed URL params, S3 error XML). */
export function scrubPatterns(text) {
  return String(text)
    .replace(/eyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]*/g, '[REDACTED_JWT]')
    .replace(/(Bearer|Basic)\s+[A-Za-z0-9._~+/=-]+/gi, '$1 [REDACTED]')
    .replace(/([?&](?:X-Amz-[A-Za-z-]+|Signature|Policy|Key-Pair-Id|token|access_token|refresh_token|sig|Expires)=)[^&\s"'<]+/gi, '$1[REDACTED]')
    .replace(/<(SignatureProvided|StringToSign|StringToSignBytes|CanonicalRequest|CanonicalRequestBytes|AWSAccessKeyId|HostId)>[\s\S]*?<\/\1>/g, '<$1>[REDACTED]</$1>')
    .replace(/\b(AKIA|ASIA)[A-Z0-9]{16}\b/g, '[REDACTED_KEY]')
    .replace(/("?(?:access_token|refresh_token|password|token)"?\s*[:=]\s*"?)[^"\s,}&]+/gi, '$1[REDACTED]');
}

/** Removes every literal occurrence of a known secret. */
export function scrubSecrets(text, secrets) {
  let out = String(text);
  for (const s of secrets || []) {
    if (typeof s === 'string' && s.length >= 4) out = out.split(s).join('[REDACTED]');
  }
  return out;
}

/** A response body made safe to print: redacted, scrubbed, single line, truncated. */
export function summarize(body, secrets = [], max = BODY_LIMIT) {
  let text;
  if (body === undefined || body === null || body === '') return '<empty body>';
  if (typeof body === 'string') {
    let parsed;
    try {
      parsed = JSON.parse(body);
    } catch {
      parsed = undefined;
    }
    text = parsed === undefined ? scrubPatterns(body) : JSON.stringify(redact(parsed));
  } else {
    text = JSON.stringify(redact(body));
  }
  text = scrubSecrets(text, secrets).replace(/\s+/g, ' ').trim();
  return text.length > max ? `${text.slice(0, max)}...` : text;
}

export const STEPS = [
  'health.gateway', 'health.features', 'health.auth', 'health.social', 'health.shorts', 'health.longvideo', 'health.shop',
  'login', 'me', 'feed',
  'post.create', 'post.heart', 'post.comment', 'post.comment-read',
  'follow.resolve', 'follow.check', 'follow.follow', 'follow.verify',
  'video.init', 'video.upload', 'video.confirm', 'video.ready', 'video.post', 'video.publish', 'video.play',
  'shop.home', 'shop.pick', 'shop.add', 'shop.quote',
  'cleanup.bag', 'cleanup.comment', 'cleanup.post', 'cleanup.video-post', 'cleanup.media', 'cleanup.unfollow',
  'logout',
];
export const GROUPS = [...new Set(STEPS.map((s) => s.split('.')[0]))];

function splitList(v) {
  return String(v || '').split(',').map((s) => s.trim()).filter(Boolean);
}

/** Parses argv into options. Throws Error with a usage message on bad input. */
export function parseArgs(argv) {
  const opts = { api: DEFAULT_API, only: [], skip: [], keep: false, video: null, followUser: DEFAULT_FOLLOW_USER, videoTimeoutSec: 300, help: false };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    const eq = a.indexOf('=');
    const flag = a.startsWith('--') && eq > 0 ? a.slice(0, eq) : a;
    const inline = a.startsWith('--') && eq > 0 ? a.slice(eq + 1) : undefined;
    const next = () => {
      if (inline !== undefined) return inline;
      if (i + 1 >= argv.length) throw new Error(`${flag} needs a value`);
      return argv[++i];
    };
    switch (flag) {
      case '--api': opts.api = next(); break;
      case '--only': opts.only.push(...splitList(next())); break;
      case '--skip': opts.skip.push(...splitList(next())); break;
      case '--keep': opts.keep = true; break;
      case '--video': opts.video = next(); break;
      case '--follow-user': opts.followUser = next(); break;
      case '--video-timeout': {
        const n = Number(next());
        if (!Number.isFinite(n) || n <= 0) throw new Error('--video-timeout must be a positive number of seconds');
        opts.videoTimeoutSec = n;
        break;
      }
      case '-h': case '--help': opts.help = true; break;
      default: throw new Error(`unknown argument: ${a}`);
    }
  }
  for (const name of [...opts.only, ...opts.skip]) {
    if (!STEPS.includes(name) && !GROUPS.includes(name)) {
      throw new Error(`unknown step "${name}" (groups: ${GROUPS.join(', ')})`);
    }
  }
  return opts;
}

/** Whether a step runs under --only/--skip. Groups match their sub-steps. */
export function isSelected(step, only, skip) {
  const group = step.split('.')[0];
  const hit = (list) => list.includes(step) || list.includes(group);
  if (hit(skip)) return false;
  if (only.length === 0) return true;
  if (hit(only)) return true;
  // login (and the logout that pairs with it) come along with any step that needs a session.
  if (step === 'login' || step === 'logout') {
    return only.some((s) => !s.startsWith('health') && s !== 'cleanup' && !s.startsWith('cleanup.'));
  }
  // Cleanup undoes whatever the selected steps created.
  if (group === 'cleanup') return true;
  return false;
}

export function mimeFor(file) {
  const ext = path.extname(file).toLowerCase();
  return { '.mp4': 'video/mp4', '.m4v': 'video/mp4', '.webm': 'video/webm', '.mov': 'video/quicktime' }[ext] || null;
}

export function isUuid(v) {
  return typeof v === 'string' && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(v);
}

/** GET /v1/posts/trending answers {items:[...], next_cursor}; every item must be of the asked kind. */
export function trendingNote(res, kind, onBad) {
  const items = res.data && Array.isArray(res.data.items) ? res.data.items : null;
  if (!items) return onBad('data.items is not a list');
  const wrong = items.find((p) => !p || !isUuid(p.id) || (p.content_type && p.content_type !== kind));
  if (wrong) return onBad(`an item is not a ${kind}`);
  return `${items.length} items`;
}

// ── Runner ───────────────────────────────────────────────────────────────

class StepError extends Error {}
const fail = (msg) => { throw new StepError(msg); };

class Smoke {
  constructor(opts, origin) {
    this.opts = opts;
    this.origin = origin;
    this.status = new Map();
    this.failed = 0;
    this.secrets = [];
    this.token = null;
    this.refreshToken = null;
    this.refreshed = false;
    this.me = null;
    this.created = { post: null, comment: null, videoPost: null, media: null, follow: null, bagVariant: null };
    this.aborted = false;
    this.runId = new Date().toISOString().replace(/[-:.TZ]/g, '').slice(0, 14);
  }

  addSecret(s) {
    if (typeof s === 'string' && s.length >= 4 && !this.secrets.includes(s)) this.secrets.push(s);
  }

  safe(body) {
    return summarize(body, this.secrets);
  }

  print(line) {
    process.stdout.write(`${scrubSecrets(line, this.secrets)}\n`);
  }

  async step(name, deps, fn) {
    if (!isSelected(name, this.opts.only, this.opts.skip)) return undefined;
    if (this.aborted && !name.startsWith('cleanup.') && name !== 'logout') {
      this.status.set(name, 'skip');
      this.print(`SKIP ${name} (interrupted)`);
      return undefined;
    }
    for (const dep of deps) {
      if (this.status.get(dep) !== 'pass') {
        this.status.set(name, 'skip');
        this.print(`SKIP ${name} (needs ${dep})`);
        return undefined;
      }
    }
    const t0 = Date.now();
    try {
      const note = await fn();
      if (note && typeof note === 'object' && note.skip) {
        this.status.set(name, 'skip');
        this.print(`SKIP ${name} (${note.skip})`);
        return undefined;
      }
      this.status.set(name, 'pass');
      this.print(`PASS ${name} (${Date.now() - t0}ms)${note ? ` ${note}` : ''}`);
      return true;
    } catch (err) {
      this.status.set(name, 'fail');
      this.failed++;
      let reason = err instanceof StepError ? err.message : `script error: ${err && err.message ? err.message : String(err)}`;
      reason = scrubPatterns(scrubSecrets(reason, this.secrets)).replace(/\s+/g, ' ');
      if (reason.length > BODY_LIMIT + 120) reason = `${reason.slice(0, BODY_LIMIT + 120)}...`;
      this.print(`FAIL ${name} (${Date.now() - t0}ms) ${reason}`);
      return false;
    }
  }

  ok(name) {
    return this.status.get(name) === 'pass';
  }

  // One request to the API origin. The token never leaves this origin.
  async api(method, pathname, { body, auth = true, headers = {}, redirect = 'follow', timeoutMs = 30000, retried = false } = {}) {
    const url = new URL(pathname, this.origin);
    if (url.origin !== this.origin) throw new Error('refusing to send a request off the API origin');
    const h = { Accept: 'application/json', ...headers };
    if (body !== undefined) h['Content-Type'] = 'application/json';
    if (auth) {
      if (!this.token) fail('no session (login did not pass)');
      h.Authorization = `Bearer ${this.token}`;
    }
    let res;
    try {
      res = await fetch(url, {
        method,
        headers: h,
        body: body === undefined ? undefined : JSON.stringify(body),
        redirect,
        signal: AbortSignal.timeout(timeoutMs),
      });
    } catch (err) {
      fail(`${method} ${url.pathname}: network error: ${err && err.cause && err.cause.code ? err.cause.code : (err && err.name) || 'unknown'}`);
    }
    const text = await res.text();
    let json;
    try {
      json = text ? JSON.parse(text) : undefined;
    } catch {
      json = undefined;
    }
    if (res.status === 401 && auth && !retried && this.refreshToken && !this.refreshed) {
      // The access token expired mid-run (the video wait is long). Refresh is
      // not a login and has its own per-token limiter; do it at most once.
      this.refreshed = true;
      if (await this.refresh()) return this.api(method, pathname, { body, auth, headers, redirect, timeoutMs, retried: true });
    }
    return { status: res.status, headers: res.headers, text, json, data: json ? json.data : undefined };
  }

  expect(res, statuses, what) {
    if (!statuses.includes(res.status)) fail(`${what}: HTTP ${res.status} ${this.safe(res.text)}`);
    return res;
  }

  async refresh() {
    try {
      const res = await fetch(new URL('/v1/auth/refresh', this.origin), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
        body: JSON.stringify({ refresh_token: this.refreshToken }),
        signal: AbortSignal.timeout(30000),
      });
      const json = await res.json().catch(() => undefined);
      const tokens = json && json.data && json.data.tokens;
      if (res.status === 200 && tokens && tokens.access_token) {
        this.token = tokens.access_token;
        this.addSecret(this.token);
        if (tokens.refresh_token) {
          this.refreshToken = tokens.refresh_token;
          this.addSecret(this.refreshToken);
        }
        return true;
      }
    } catch {
      // fall through: the original 401 is reported by the caller
    }
    return false;
  }

  // ── Steps ──────────────────────────────────────────────────────────────

  async health() {
    const pub = (p) => this.api('GET', p, { auth: false });
    await this.step('health.gateway', [], async () => {
      const h = this.expect(await pub('/health'), [200], 'GET /health');
      if (!h.json || h.json.status !== 'ok') fail(`GET /health: unexpected body ${this.safe(h.text)}`);
      const r = this.expect(await pub('/readyz'), [200], 'GET /readyz');
      if (!r.json || r.json.status !== 'ready') fail(`GET /readyz: unexpected body ${this.safe(r.text)}`);
      return `routes=${r.json.routes}`;
    });
    await this.step('health.features', [], async () => {
      const r = this.expect(await pub('/v1/features'), [200], 'GET /v1/features');
      const products = r.data && r.data.products;
      if (!products || typeof products !== 'object') fail(`GET /v1/features: no data.products ${this.safe(r.text)}`);
      return `${Object.keys(products).length} products listed`;
    });
    await this.step('health.auth', [], async () => {
      this.expect(await pub('/v1/auth/health'), [200], 'GET /v1/auth/health');
    });
    await this.step('health.social', [], async () => {
      const r = this.expect(await pub('/v1/hashtags/trending?limit=5'), [200], 'GET /v1/hashtags/trending');
      if (r.json === undefined) fail('GET /v1/hashtags/trending: body is not JSON');
    });
    await this.step('health.shorts', [], async () => {
      const r = this.expect(await pub('/v1/posts/trending?content_type=flick&limit=5'), [200], 'GET /v1/posts/trending?content_type=flick');
      return trendingNote(r, 'flick', (m) => fail(`trending flicks: ${m} ${this.safe(r.text)}`));
    });
    await this.step('health.longvideo', [], async () => {
      const r = this.expect(await pub('/v1/posts/trending?content_type=long_video&limit=5'), [200], 'GET /v1/posts/trending?content_type=long_video');
      return trendingNote(r, 'long_video', (m) => fail(`trending long videos: ${m} ${this.safe(r.text)}`));
    });
    await this.step('health.shop', [], async () => {
      const r = this.expect(await pub('/v1/commerce/categories'), [200], 'GET /v1/commerce/categories');
      if (r.json === undefined) fail('GET /v1/commerce/categories: body is not JSON');
    });
  }

  async login() {
    await this.step('login', [], async () => {
      const file = process.env.HARNESS_ACCOUNT_FILE || DEFAULT_ACCOUNT_FILE;
      if (!existsSync(file)) fail(`account file not found (${path.basename(file)}); set HARNESS_ACCOUNT_FILE`);
      const env = parseEnv(readFileSync(file, 'utf8'));
      const identifier = env.HARNESS_TEST_USERNAME;
      const password = env.HARNESS_TEST_PASSWORD;
      if (!identifier || !password) fail('account file lacks HARNESS_TEST_USERNAME or HARNESS_TEST_PASSWORD');
      this.addSecret(password);
      this.addSecret(identifier); // an email address: kept out of output too
      // auth-service resolves `identifier` as a phone, then an email; it has no username lookup.
      const res = await this.api('POST', '/v1/auth/login', { auth: false, body: { identifier, password } });
      this.expect(res, [200], 'POST /v1/auth/login');
      const d = res.data || {};
      if (d.requires_2fa) fail('account requires 2FA; the smoke account must not have it');
      if (d.requires_step_up) fail(`login asked for an anomaly step-up (${(d.step_up_methods || []).join('/') || 'no methods'})`);
      const tokens = d.tokens || {};
      if (!tokens.access_token) fail(`login returned no access token ${this.safe(res.text)}`);
      this.token = tokens.access_token;
      this.refreshToken = tokens.refresh_token || null;
      this.addSecret(this.token);
      this.addSecret(this.refreshToken);
      if (d.user && isUuid(d.user.id)) this.me = d.user.id;
    });
  }

  async meStep() {
    await this.step('me', ['login'], async () => {
      const r = this.expect(await this.api('GET', '/v1/auth/me'), [200], 'GET /v1/auth/me');
      const id = r.data && (r.data.user_id || r.data.id);
      if (!isUuid(id)) fail(`GET /v1/auth/me: no user id ${this.safe(r.text)}`);
      if (this.me && this.me !== id) fail('GET /v1/auth/me: id differs from the login response');
      this.me = id;
      return `account_status=${r.data.account_status || 'unknown'}`;
    });
  }

  async feed() {
    await this.step('feed', ['login'], async () => {
      const r = this.expect(await this.api('GET', '/v1/feed/home?limit=10'), [200], 'GET /v1/feed/home');
      if (!r.json) fail('GET /v1/feed/home: body is not JSON');
      const items = r.data === null ? [] : r.data;
      if (!Array.isArray(items)) fail(`GET /v1/feed/home: data is not a list ${this.safe(r.text)}`);
      const bad = items.find((p) => !p || !isUuid(p.id));
      if (bad) fail(`GET /v1/feed/home: an item has no id ${this.safe(bad)}`);
      const empty = r.headers.get('x-feed-empty-reason');
      return `${items.length} items, mode=${r.headers.get('x-feed-mode') || '?'}${empty ? `, empty_reason=${empty}` : ''}`;
    });
  }

  async posts() {
    const text = `dev-smoke ${this.runId} (automated, deleted at the end of the run)`;
    await this.step('post.create', ['login'], async () => {
      // `private` is "only you": the least public value, and the author may
      // still heart and comment on it (viewerMayViewPost lets the author in).
      const res = await this.api('POST', '/v1/posts', {
        headers: { 'Idempotency-Key': randomUUID() },
        body: { text, visibility: 'private', content_type: 'post' },
      });
      this.expect(res, [201], 'POST /v1/posts');
      const id = res.data && res.data.id;
      if (!isUuid(id)) fail(`POST /v1/posts: no post id ${this.safe(res.text)}`);
      this.created.post = id;
      if (res.data.visibility && res.data.visibility !== 'private') fail(`POST /v1/posts: stored visibility is ${res.data.visibility}, not private`);
      return `post ${id}`;
    });
    await this.step('post.heart', ['post.create'], async () => {
      const r = this.expect(await this.api('POST', `/v1/posts/${this.created.post}/like`), [200], 'POST /v1/posts/:id/like');
      if (!r.data || r.data.liked !== true) fail(`like toggle did not leave the post hearted ${this.safe(r.text)}`);
      return `count=${r.data.count}`;
    });
    const commentText = `dev-smoke comment ${this.runId}`;
    await this.step('post.comment', ['post.create'], async () => {
      const r = await this.api('POST', `/v1/posts/${this.created.post}/comments`, {
        headers: { 'Idempotency-Key': randomUUID() },
        body: { text: commentText },
      });
      this.expect(r, [201], 'POST /v1/posts/:id/comments');
      const id = r.data && r.data.id;
      if (!isUuid(id)) fail(`no comment id ${this.safe(r.text)}`);
      this.created.comment = id;
      return `comment ${id}`;
    });
    await this.step('post.comment-read', ['post.comment'], async () => {
      const r = this.expect(await this.api('GET', `/v1/posts/${this.created.post}/comments?limit=20`), [200], 'GET /v1/posts/:id/comments');
      const list = Array.isArray(r.data) ? r.data : (r.data && Array.isArray(r.data.items) ? r.data.items : null);
      if (!list) fail(`comment list is not a list ${this.safe(r.text)}`);
      const mine = list.find((c) => c && c.id === this.created.comment);
      if (!mine) fail(`comment ${this.created.comment} not in the first page (${list.length} rows)`);
      const body = mine.body !== undefined ? mine.body : mine.text;
      if (body !== commentText) fail(`comment text read back differs: ${this.safe(String(body))}`);
    });
  }

  async relationship(targetId) {
    const r = this.expect(
      await this.api('GET', `/v1/graph/relationship?user_id=${this.me}&other_id=${targetId}`),
      [200], 'GET /v1/graph/relationship');
    if (!r.data || typeof r.data.follows !== 'boolean') fail(`relationship has no follows flag ${this.safe(r.text)}`);
    return r.data;
  }

  async follow() {
    let targetId = null;
    let before = null;
    const user = this.opts.followUser;
    await this.step('follow.resolve', ['login'], async () => {
      // app user-service is the username authority (identity-profile 404s).
      const r = this.expect(await this.api('GET', `/v1/users/by-username/${encodeURIComponent(user)}`), [200], `GET /v1/users/by-username/${user}`);
      targetId = r.data && r.data.id;
      if (!isUuid(targetId)) fail(`no id for ${user} ${this.safe(r.text)}`);
      return `${user}=${targetId}`;
    });
    await this.step('follow.check', ['follow.resolve', 'me'], async () => {
      before = await this.relationship(targetId);
      if (before.follows) return 'already following before this run; left in place';
      if (before.follow_request_status === 'pending_sent') return 'a follow request was already pending; left in place';
      return 'not following';
    });
    const preexisting = before && (before.follows || before.follow_request_status === 'pending_sent');
    await this.step('follow.follow', ['follow.check'], async () => {
      if (preexisting) return { skip: 'already following before this run' };
      const r = this.expect(await this.api('POST', '/v1/graph/follow', { body: { user_id: targetId } }), [200], 'POST /v1/graph/follow');
      const status = r.data && r.data.status;
      if (status !== 'followed' && status !== 'requested') fail(`unexpected follow status ${this.safe(r.text)}`);
      this.created.follow = { targetId, status };
      return `status=${status}`;
    });
    await this.step('follow.verify', [preexisting ? 'follow.check' : 'follow.follow'], async () => {
      const rel = await this.relationship(targetId);
      const expectRequested = this.created.follow ? this.created.follow.status === 'requested' : (!before.follows && before.follow_request_status === 'pending_sent');
      if (expectRequested) {
        if (rel.follow_request_status !== 'pending_sent') fail(`expected a pending request, got follows=${rel.follows} follow_request_status=${rel.follow_request_status}`);
        return 'request pending (private account)';
      }
      if (!rel.follows) fail(`relationship says not following (follow_request_status=${rel.follow_request_status})`);
      return 'follows=true';
    });
  }

  resolveVideo() {
    const pick = this.opts.video || process.env.DEV_SMOKE_VIDEO;
    const candidates = pick ? [pick] : VIDEO_CANDIDATES.map((c) => path.join(REPO_ROOT, c));
    for (const c of candidates) {
      const abs = path.resolve(c);
      if (existsSync(abs) && statSync(abs).isFile()) return abs;
    }
    return null;
  }

  async video() {
    let file = null;
    let mime = null;
    let bytes = null;
    let uploadUrl = null;
    let lastStatus = null;
    await this.step('video.init', ['login'], async () => {
      file = this.resolveVideo();
      if (!file) fail(`no video fixture found (tried ${VIDEO_CANDIDATES.join(', ')}); pass --video <file>`);
      mime = mimeFor(file);
      if (!mime) fail(`unsupported video extension: ${path.basename(file)}`);
      bytes = readFileSync(file);
      const r = await this.api('POST', '/v1/media/init', {
        body: { file_type: 'video', mime_type: mime, file_size_bytes: bytes.length, media_subtype: 'general', upload_purpose: 'composer' },
      });
      this.expect(r, [200, 201], 'POST /v1/media/init');
      const d = r.data || {};
      if (!isUuid(d.media_id) || typeof d.upload_url !== 'string') fail(`init returned no media_id/upload_url ${this.safe(r.text)}`);
      this.created.media = d.media_id;
      uploadUrl = d.upload_url.replace(/\\u0026/g, '&');
      return `media ${d.media_id} (${path.basename(file)}, ${bytes.length} bytes)`;
    });
    await this.step('video.upload', ['video.init'], async () => {
      let target;
      try {
        target = new URL(uploadUrl);
      } catch {
        fail('upload_url is not a URL');
      }
      if (!storageHostIsDev(target.hostname)) fail(`upload_url host ${target.hostname} is not a dev host; not uploading`);
      // Straight to storage, signed URL, no Authorization header.
      let res;
      try {
        res = await fetch(target, { method: 'PUT', headers: { 'Content-Type': mime }, body: bytes, signal: AbortSignal.timeout(120000) });
      } catch (err) {
        fail(`PUT upload_url: network error: ${err && err.cause && err.cause.code ? err.cause.code : (err && err.name) || 'unknown'}`);
      }
      const text = await res.text();
      if (res.status !== 200 && res.status !== 204) fail(`PUT upload_url (${target.hostname}): HTTP ${res.status} ${this.safe(text)}`);
      return `to ${target.hostname}`;
    });
    await this.step('video.confirm', ['video.upload'], async () => {
      const r = await this.api('POST', '/v1/media/confirm', { body: { media_id: this.created.media } });
      this.expect(r, [200, 201, 202], 'POST /v1/media/confirm');
    });
    await this.step('video.ready', ['video.confirm'], async () => {
      const deadline = Date.now() + this.opts.videoTimeoutSec * 1000;
      for (;;) {
        if (this.aborted) fail('interrupted while waiting for the transcode');
        const r = await this.api('GET', `/v1/media/${this.created.media}/status`);
        this.expect(r, [200], 'GET /v1/media/:id/status');
        const d = r.data || {};
        lastStatus = `processing_status=${d.processing_status} moderation_status=${d.moderation_status}`;
        if (d.processing_status === 'ready' && d.moderation_status === 'passed') {
          return `${lastStatus}${d.duration_ms ? ` duration_ms=${d.duration_ms}` : ''}`;
        }
        if (['failed', 'rejected'].includes(d.processing_status) || d.moderation_status === 'rejected') fail(`stopped at ${lastStatus}`);
        if (Date.now() > deadline) fail(`timed out after ${this.opts.videoTimeoutSec}s at ${lastStatus}`);
        await new Promise((resolve) => setTimeout(resolve, 3000));
      }
    });
    await this.step('video.post', ['video.ready'], async () => {
      // A short clip is a Short (flick, <= 10 min). It is created explicitly
      // as a flick, so the long-video guards (title, channel) do not apply,
      // and private so nobody else sees it.
      const r = await this.api('POST', '/v1/posts', {
        headers: { 'Idempotency-Key': randomUUID() },
        body: {
          text: `dev-smoke video ${this.runId}`,
          title: `dev-smoke ${this.runId}`,
          visibility: 'private',
          content_type: 'flick',
          media_ids: [this.created.media],
        },
      });
      this.expect(r, [201], 'POST /v1/posts (flick)');
      const id = r.data && r.data.id;
      if (!isUuid(id)) fail(`no post id ${this.safe(r.text)}`);
      this.created.videoPost = id;
      return `post ${id} content_type=${r.data.content_type || '?'}`;
    });
    await this.step('video.publish', ['video.post'], async () => {
      // Publish takes the POST id, never the media id.
      const r = this.expect(await this.api('POST', `/v1/videos/${this.created.videoPost}/publish`), [200], 'POST /v1/videos/:postId/publish');
      if (!r.data || r.data.status !== 'published') fail(`publish answered ${this.safe(r.text)}`);
    });
    await this.step('video.play', ['video.post'], async () => {
      const u = this.expect(await this.api('GET', `/v1/media/${this.created.media}/url`), [200], 'GET /v1/media/:id/url');
      const d = u.data || {};
      if (!d.playback_url) fail(`no playback_url ${this.safe(u.text)}`);
      const kind = d.playback_kind || '?';
      if (d.playback_url.startsWith('/')) {
        // The authorized HLS master playlist on the API origin.
        const r = await this.api('GET', d.playback_url, { headers: { Accept: '*/*' } });
        const ct = r.headers.get('content-type') || '';
        if (r.status !== 200) fail(`GET ${kind} playlist: HTTP ${r.status} ${this.safe(r.text)}`);
        if (!/mpegurl/i.test(ct) || !r.text.startsWith('#EXTM3U')) fail(`GET ${kind} playlist: content-type ${ct}, body does not start with #EXTM3U`);
        return `kind=${kind} content-type=${ct}`;
      }
      let target;
      try {
        target = new URL(d.playback_url);
      } catch {
        fail('playback_url is not a URL');
      }
      if (!storageHostIsDev(target.hostname)) fail(`playback host ${target.hostname} is not a dev host; not fetching`);
      // A signed object URL: no Authorization header, first KiB only.
      const res = await fetch(target, { headers: { Range: 'bytes=0-1023' }, signal: AbortSignal.timeout(30000) });
      const ct = res.headers.get('content-type') || '';
      await res.arrayBuffer().catch(() => undefined);
      if (res.status !== 200 && res.status !== 206) fail(`GET ${kind} playback (${target.hostname}): HTTP ${res.status}`);
      if (!/^video\/|mpegurl/i.test(ct)) fail(`GET ${kind} playback: content-type ${ct || '<none>'} is not video`);
      return `kind=${kind} HTTP ${res.status} content-type=${ct}`;
    });
  }

  async shop() {
    let pick = null;
    await this.step('shop.home', ['login'], async () => {
      const r = this.expect(await this.api('GET', '/v1/commerce/home'), [200], 'GET /v1/commerce/home');
      const d = r.data || {};
      if (!Array.isArray(d.sections) || !Array.isArray(d.banners)) fail(`home page has no sections/banners lists ${this.safe(r.text)}`);
      return `${d.sections.length} sections, ${d.banners.length} banners`;
    });
    await this.step('shop.pick', ['login'], async () => {
      const cart = this.expect(await this.api('GET', '/v1/commerce/cart'), [200], 'GET /v1/commerce/cart');
      const cartItems = (cart.data && Array.isArray(cart.data.items)) ? cart.data.items : [];
      const inCart = new Set(cartItems.map((l) => l.variant_id));
      const cartSeller = cart.data && cart.data.seller_id;
      if (cartItems.length > 0 && !cartSeller) fail('the bag already holds lines from more than one seller; empty it first');
      const list = this.expect(await this.api('GET', '/v1/commerce/products?in_stock=true&limit=20'), [200], 'GET /v1/commerce/products');
      const products = (list.data && Array.isArray(list.data.items)) ? list.data.items : [];
      if (products.length === 0) fail('no in-stock products listed on the storefront');
      let looked = 0;
      for (const p of products) {
        if (!isUuid(p.id)) continue;
        if (cartSeller && p.seller_id && p.seller_id !== cartSeller) continue;
        if (looked >= 8) break;
        looked++;
        const r = await this.api('GET', `/v1/commerce/products/${p.id}`);
        if (r.status !== 200) continue;
        const variants = (r.data && Array.isArray(r.data.variants)) ? r.data.variants : [];
        const v = variants.find((x) => x && isUuid(x.id) && (!x.status || x.status === 'active') && typeof x.available_qty === 'number' && x.available_qty >= 1 && !inCart.has(x.id));
        if (v) {
          pick = { productId: p.id, variantId: v.id };
          return `variant ${v.id} of product ${p.id} (available ${v.available_qty})`;
        }
      }
      fail(`no active in-stock variant found in ${looked} products${cartSeller ? ' from the seller already in the bag' : ''}`);
    });
    await this.step('shop.add', ['shop.pick'], async () => {
      const r = await this.api('POST', '/v1/commerce/cart/items', { body: { variant_id: pick.variantId, quantity: 1 } });
      this.expect(r, [200, 201], 'POST /v1/commerce/cart/items');
      this.created.bagVariant = pick.variantId;
      const items = (r.data && Array.isArray(r.data.items)) ? r.data.items : [];
      const line = items.find((l) => l.variant_id === pick.variantId);
      if (!line) fail('the bag returned by add-to-bag has no line for the variant');
      return `bag subtotal_minor=${r.data.subtotal_minor}`;
    });
    await this.step('shop.quote', ['shop.add'], async () => {
      const a = this.expect(await this.api('GET', '/v1/commerce/addresses'), [200], 'GET /v1/commerce/addresses');
      const addrs = Array.isArray(a.data) ? a.data : ((a.data && Array.isArray(a.data.items)) ? a.data.items : []);
      if (addrs.length === 0) fail('the test account has no saved address; POST /v1/commerce/checkout/quote needs address_id');
      const addr = addrs.find((x) => x.is_default) || addrs[0];
      // A price quote only. Checkout and payment intents are never called.
      const r = await this.api('POST', '/v1/commerce/checkout/quote', { body: { address_id: addr.id } });
      this.expect(r, [200], 'POST /v1/commerce/checkout/quote');
      const d = r.data || {};
      if (!isUuid(d.quote_id) || typeof d.total_minor !== 'number') fail(`quote has no quote_id/total_minor ${this.safe(r.text)}`);
      return `total_minor=${d.total_minor} ${d.currency || ''}`.trim();
    });
  }

  async cleanup() {
    const c = this.created;
    if (this.opts.keep || this.opts.skip.includes('cleanup')) {
      for (const [what, v] of Object.entries(c)) {
        if (v) this.print(`KEEP ${what} ${typeof v === 'object' ? `${v.targetId} (${v.status})` : v}`);
      }
      return;
    }
    if (!this.token) return;
    if (c.bagVariant) {
      await this.step('cleanup.bag', [], async () => {
        const r = this.expect(await this.api('DELETE', `/v1/commerce/cart/items/${c.bagVariant}`), [200, 204], 'DELETE /v1/commerce/cart/items/:variantId');
        const items = (r.data && Array.isArray(r.data.items)) ? r.data.items : [];
        if (items.some((l) => l.variant_id === c.bagVariant)) fail('the line is still in the bag');
      });
    }
    if (c.comment) {
      await this.step('cleanup.comment', [], async () => {
        this.expect(await this.api('DELETE', `/v1/comments/${c.comment}`), [200, 204], 'DELETE /v1/comments/:id');
      });
    }
    if (c.post) {
      await this.step('cleanup.post', [], async () => {
        this.expect(await this.api('DELETE', `/v1/posts/${c.post}`), [200, 204], 'DELETE /v1/posts/:id');
      });
    }
    if (c.videoPost) {
      await this.step('cleanup.video-post', [], async () => {
        this.expect(await this.api('DELETE', `/v1/posts/${c.videoPost}`), [200, 204], 'DELETE /v1/posts/:id (video)');
      });
    } else if (c.media) {
      // Uploaded but never attached to a post: remove the orphan asset.
      await this.step('cleanup.media', [], async () => {
        this.expect(await this.api('DELETE', `/v1/media/${c.media}`), [200, 204], 'DELETE /v1/media/:id');
      });
    }
    if (c.follow) {
      await this.step('cleanup.unfollow', [], async () => {
        if (c.follow.status === 'requested') {
          this.expect(await this.api('DELETE', `/v1/graph/follow-requests/${c.follow.targetId}`), [200, 204], 'DELETE /v1/graph/follow-requests/:targetId');
          return 'request cancelled';
        }
        this.expect(await this.api('POST', '/v1/graph/unfollow', { body: { user_id: c.follow.targetId } }), [200], 'POST /v1/graph/unfollow');
      });
    }
  }

  async logout() {
    if (!this.refreshToken) return;
    await this.step('logout', [], async () => {
      // Revokes this run's session so smoke runs do not pile up sessions.
      const res = await fetch(new URL('/v1/auth/logout', this.origin), {
        method: 'POST',
        headers: { Accept: 'application/json', Cookie: `refresh_token=${this.refreshToken}` },
        signal: AbortSignal.timeout(30000),
      });
      const text = await res.text();
      if (res.status !== 200 && res.status !== 204) fail(`POST /v1/auth/logout: HTTP ${res.status} ${this.safe(text)}`);
    });
  }

  async run() {
    process.on('SIGINT', () => {
      if (this.aborted) process.exit(130);
      this.aborted = true;
      process.stderr.write('interrupted: finishing the current request, then cleaning up (Ctrl-C again to quit now)\n');
    });
    await this.health();
    await this.login();
    await this.meStep();
    await this.feed();
    await this.posts();
    await this.follow();
    await this.video();
    await this.shop();
    await this.cleanup();
    await this.logout();
    if (this.failed === 0) {
      this.print('RESULT PASS');
      return 0;
    }
    this.print(`RESULT FAIL (${this.failed} failed)`);
    return 1;
  }
}

const USAGE = `usage: bash scripts/smoke/dev-smoke.sh [--api <url>] [--only <step,...>] [--skip <step,...>]
                                      [--keep] [--video <file>] [--follow-user <username>]
                                      [--video-timeout <seconds>]
steps or groups: ${GROUPS.join(', ')}
see docs/harness/DEV-SMOKE.md`;

export async function main(argv) {
  let opts;
  try {
    opts = parseArgs(argv);
  } catch (err) {
    process.stderr.write(`${err.message}\n${USAGE}\n`);
    return 2;
  }
  if (opts.help) {
    process.stdout.write(`${USAGE}\n`);
    return 0;
  }
  const target = checkTarget(opts.api);
  if (!target.ok) {
    process.stdout.write(`REFUSED: ${target.host} is not a dev host${target.reason !== 'not a dev host' ? ` (${target.reason})` : ''}\n`);
    return 2;
  }
  const major = Number(process.versions.node.split('.')[0]);
  if (major < 20) {
    process.stderr.write(`node ${process.versions.node} is too old; need 20+\n`);
    return 2;
  }
  process.stdout.write(`dev-smoke against ${target.origin}\n`);
  return new Smoke(opts, target.origin).run();
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  main(process.argv.slice(2)).then(
    (code) => { process.exitCode = code; },
    (err) => {
      process.stdout.write(`FAIL runner (0ms) script error: ${scrubPatterns(err && err.message ? err.message : String(err))}\nRESULT FAIL (1 failed)\n`);
      process.exitCode = 1;
    },
  );
}

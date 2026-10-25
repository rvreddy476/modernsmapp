// Network-free tests for the pure parts of dev-smoke.mjs.
//   node --test scripts/smoke/dev-smoke.test.mjs
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import {
  hostIsDev, storageHostIsDev, checkTarget, parseEnv, redact, scrubPatterns, scrubSecrets, summarize,
  parseArgs, isSelected, mimeFor, isUuid, BODY_LIMIT, DEFAULT_API,
} from './dev-smoke.mjs';

const SCRIPT = path.join(path.dirname(fileURLToPath(import.meta.url)), 'dev-smoke.mjs');

test('dev hosts are accepted', () => {
  for (const h of ['api-dev.cleestudio.com', 'media-dev.cleestudio.com', 'api.dev.example.com', 'dev.example.com',
    'dev-api.example.com', 'localhost', '127.0.0.1', '::1', 'app.localhost']) {
    assert.equal(hostIsDev(h), true, h);
  }
});

test('prod, staging and look-alike hosts are refused', () => {
  for (const h of ['api.cleestudio.com', 'cleestudio.com', 'api-staging.cleestudio.com', 'staging-dev.example.com',
    'api.prod.dev.example.com', 'devious.example.com', 'apidev.example.com', 'developer.example.com', 'dev',
    '10.0.0.5', '', undefined]) {
    assert.equal(hostIsDev(h), false, String(h));
  }
});

test('storage hosts: the dev bucket passes, prod buckets do not, and it never widens the API check', () => {
  assert.equal(storageHostIsDev('atpost-devbox-media-551626543760.s3.dualstack.ap-south-1.amazonaws.com'), true);
  assert.equal(storageHostIsDev('media-dev.cleestudio.com'), true);
  assert.equal(storageHostIsDev('atpost-prod-media-551626543760.s3.ap-south-1.amazonaws.com'), false);
  assert.equal(storageHostIsDev('atpost-devbox-prod.s3.amazonaws.com'), false);
  assert.equal(storageHostIsDev('atpost-media.s3.amazonaws.com'), false);
  assert.equal(storageHostIsDev('developer-media.s3.amazonaws.com'), false);
  assert.equal(hostIsDev('atpost-devbox-media-551626543760.s3.dualstack.ap-south-1.amazonaws.com'), false);
});

test('checkTarget refuses prod and plain http off localhost', () => {
  assert.equal(checkTarget(DEFAULT_API).ok, true);
  assert.deepEqual(checkTarget('https://api.cleestudio.com'), { ok: false, host: 'api.cleestudio.com', reason: 'not a dev host' });
  assert.equal(checkTarget('http://api-dev.cleestudio.com').ok, false);
  assert.equal(checkTarget('http://localhost:8080').ok, true);
  assert.equal(checkTarget('https://user:pw@api-dev.cleestudio.com').ok, false);
  assert.equal(checkTarget('not a url').ok, false);
  assert.equal(checkTarget('https://api-dev.cleestudio.com/some/path').origin, 'https://api-dev.cleestudio.com');
});

test('the refusal happens before any request and exits 2', () => {
  const r = spawnSync(process.execPath, [SCRIPT, '--api', 'https://api.cleestudio.com'], { encoding: 'utf8', timeout: 10000 });
  assert.equal(r.status, 2);
  assert.equal(r.stdout.trim(), 'REFUSED: api.cleestudio.com is not a dev host');
});

test('parseEnv handles CRLF, comments, export, quotes and special characters', () => {
  const env = parseEnv('﻿# comment\r\nexport A=1\r\nB = "two words"\r\nC=\'q\'\r\nD=p@ss#wo=rd\r\n\r\nnot a line\r\nE=\r\n');
  assert.deepEqual(env, { A: '1', B: 'two words', C: 'q', D: 'p@ss#wo=rd', E: '' });
});

test('redact replaces sensitive keys at any depth', () => {
  const out = redact({
    data: {
      tokens: { access_token: 'a', refresh_token: 'b' },
      user: { id: 'u1', email: 'x@y.z', phone: '1' },
      password: 'p', Authorization: 'Bearer z', cookie: 'c', client_secret: 's', session_id: 's1',
      list: [{ api_key: 'k', ok: 1 }],
    },
  });
  assert.equal(out.data.tokens, '[REDACTED]');
  assert.equal(out.data.user.id, 'u1');
  assert.equal(out.data.user.email, '[REDACTED]');
  for (const k of ['password', 'Authorization', 'cookie', 'client_secret', 'session_id']) assert.equal(out.data[k], '[REDACTED]', k);
  assert.deepEqual(out.data.list, [{ api_key: '[REDACTED]', ok: 1 }]);
});

test('scrubPatterns removes JWTs, bearer values, signed URL params and S3 error internals', () => {
  const jwt = 'eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxMjMifQ.c2lnbmF0dXJl';
  const s = scrubPatterns(`t=${jwt} Authorization: Bearer abc.def https://media-dev.x/k?X-Amz-Signature=deadbeef&X-Amz-Credential=AKIAABCDEFGHIJKLMNOP%2F&a=1 <SignatureProvided>zz</SignatureProvided> AKIAABCDEFGHIJKLMNOP`);
  assert.ok(!s.includes(jwt));
  assert.ok(!s.includes('abc.def'));
  assert.ok(!s.includes('deadbeef'));
  assert.ok(!s.includes('zz<'));
  assert.ok(!s.includes('AKIAABCDEFGHIJKLMNOP'));
  assert.ok(s.includes('a=1'));
});

test('scrubSecrets removes known secret values wherever they appear', () => {
  assert.equal(scrubSecrets('x hunter2!@ y hunter2!@', ['hunter2!@']), 'x [REDACTED] y [REDACTED]');
  assert.equal(scrubSecrets('abc', ['', null, 'ab']), 'abc');
});

test('summarize redacts JSON bodies, scrubs secrets and truncates', () => {
  const body = JSON.stringify({ error: { code: 'X', message: 'pw is s3cretPW!' }, data: { tokens: { access_token: 'zzz' } } });
  const out = summarize(body, ['s3cretPW!']);
  assert.ok(!out.includes('s3cretPW!'));
  assert.ok(!out.includes('zzz'));
  assert.ok(out.includes('"code":"X"'));
  const long = summarize('x'.repeat(1000));
  assert.equal(long.length, BODY_LIMIT + 3);
  assert.equal(summarize(''), '<empty body>');
  assert.ok(!summarize('{"refresh_token":"r1r1r1"}').includes('r1r1r1'));
  assert.ok(!summarize('refresh_token=r1r1r1&x=1').includes('r1r1r1'));
});

test('parseArgs reads flags and rejects unknown steps', () => {
  const o = parseArgs(['--api=http://localhost:8080', '--only', 'post,feed', '--skip', 'video', '--keep', '--video-timeout', '60']);
  assert.equal(o.api, 'http://localhost:8080');
  assert.deepEqual(o.only, ['post', 'feed']);
  assert.deepEqual(o.skip, ['video']);
  assert.equal(o.keep, true);
  assert.equal(o.videoTimeoutSec, 60);
  assert.throws(() => parseArgs(['--only', 'nope']), /unknown step/);
  assert.throws(() => parseArgs(['--bogus']), /unknown argument/);
  assert.throws(() => parseArgs(['--api']), /needs a value/);
});

test('isSelected: groups, skip wins, login and cleanup follow the selection', () => {
  assert.equal(isSelected('post.heart', [], []), true);
  assert.equal(isSelected('post.heart', ['post'], []), true);
  assert.equal(isSelected('feed', ['post'], []), false);
  assert.equal(isSelected('login', ['post'], []), true);
  assert.equal(isSelected('login', ['health'], []), false);
  assert.equal(isSelected('cleanup.post', ['post'], []), true);
  assert.equal(isSelected('video.ready', [], ['video']), false);
  assert.equal(isSelected('post.heart', ['post'], ['post.heart']), false);
});

test('small helpers', () => {
  assert.equal(mimeFor('a/b.MP4'), 'video/mp4');
  assert.equal(mimeFor('a.webm'), 'video/webm');
  assert.equal(mimeFor('a.txt'), null);
  assert.equal(isUuid('66668bc2-a3f6-40a5-9cdd-c998dcf72f29'), true);
  assert.equal(isUuid('nope'), false);
});

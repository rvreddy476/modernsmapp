// Data-file and request helpers shared by the reels and long-video scripts.
//
// Every data file is a plain text file, one row per line, columns separated
// by a TAB. Blank lines and lines starting with '#' are skipped. The README
// lists each file's columns and a read-only query that produces it from the
// staging or *_test data set. Token files hold bearer tokens: keep them
// outside the repository (the folder's .gitignore also refuses *.tokens).
//
// SharedArray must be built in the init context; call these helpers at the
// top level of a script, never inside the default function.

import { SharedArray } from 'k6/data';
import exec from 'k6/execution';

export function requireEnv(name) {
  const v = String(__ENV[name] || '').trim();
  if (!v) {
    throw new Error(`${name} is required (see Architecture/tools/loadtest/README.md)`);
  }
  return v;
}

export function envInt(name, dflt) {
  const raw = String(__ENV[name] || '').trim();
  if (!raw) return dflt;
  const n = parseInt(raw, 10);
  if (!Number.isFinite(n)) throw new Error(`${name} must be an integer, got "${raw}"`);
  return n;
}

export function envFloat(name, dflt) {
  const raw = String(__ENV[name] || '').trim();
  if (!raw) return dflt;
  const n = parseFloat(raw);
  if (!Number.isFinite(n)) throw new Error(`${name} must be a number, got "${raw}"`);
  return n;
}

// tsv loads a TAB-separated file named by envName into a SharedArray of
// arrays of strings. minRows guards against an empty or truncated export.
export function tsv(envName, minRows) {
  const path = requireEnv(envName);
  const rows = new SharedArray(envName, function () {
    return open(path)
      .split('\n')
      .map((l) => l.replace(/\r$/, ''))
      .filter((l) => l.trim().length > 0 && !l.trim().startsWith('#'))
      .map((l) => l.split('\t').map((c) => c.trim()));
  });
  if (rows.length < (minRows || 1)) {
    throw new Error(`${envName} (${path}) has ${rows.length} rows; at least ${minRows || 1} are needed`);
  }
  return rows;
}

// optionalTsv is tsv for files a scenario can run without.
export function optionalTsv(envName) {
  if (!String(__ENV[envName] || '').trim()) return null;
  return tsv(envName, 1);
}

// tokens loads a token file: either "<access_token>" or
// "<user_id>\t<access_token>" per line. Returns [{userId, token}].
export function tokens(envName, minRows) {
  const rows = tsv(envName, minRows);
  return {
    length: rows.length,
    at(i) {
      const r = rows[i % rows.length];
      return r.length >= 2 ? { userId: r[0], token: r[1] } : { userId: '', token: r[0] };
    },
  };
}

export function pick(arr) {
  return arr[Math.floor(Math.random() * arr.length)];
}

// vuToken gives each VU a stable account, so per-user server state
// (rate limits, progress rows, sessions) behaves like one person per VU.
export function vuToken(tok) {
  return tok.at(exec.vu.idInTest - 1);
}

export function authHeaders(t, extra) {
  const h = { Authorization: `Bearer ${t.token}`, Accept: 'application/json' };
  return Object.assign(h, extra || {});
}

export function jsonHeaders(t, extra) {
  return authHeaders(t, Object.assign({ 'Content-Type': 'application/json' }, extra || {}));
}

// uuidv4 for client event ids, session ids and idempotency keys. Not
// cryptographic; it only has to be unique within a run.
export function uuidv4() {
  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, (c) => {
    const r = (Math.random() * 16) | 0;
    return (c === 'x' ? r : (r & 0x3) | 0x8).toString(16);
  });
}

export function safeJson(res) {
  try {
    return res.json();
  } catch (_) {
    return null;
  }
}

// nextCursor reads meta.next_cursor, where every service in this tree puts
// the next-page cursor (shared/api/response.go).
export function nextCursor(res) {
  const body = safeJson(res);
  return body && body.meta && body.meta.next_cursor ? body.meta.next_cursor : '';
}

export function sleepJitter(min, max) {
  return min + Math.random() * (max - min);
}

// endpointThresholds builds per-endpoint latency thresholds on the
// `endpoint` tag every request in these scripts carries.
export function endpointThresholds(map) {
  const out = {};
  for (const [endpoint, limits] of Object.entries(map)) {
    out[`http_req_duration{endpoint:${endpoint}}`] = limits;
  }
  return out;
}

// scale multiplies a target rate or VU count by RATE_SCALE (default 1).
// Use RATE_SCALE=0.1 for a first shake-out run and 0.5 for the soak mixes.
export function scale(n) {
  const s = envFloat('RATE_SCALE', 1);
  return Math.max(1, Math.round(n * s));
}

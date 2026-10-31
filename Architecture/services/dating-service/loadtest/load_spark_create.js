// k6 — load test: POST /v1/dating/sparks
//
// Goal: 50 concurrent users posting Sparks; covers the match-formation
// path when the recipient has already Sparked the sender (mutual Spark
// → match created synchronously).
//
// Run:
//   BASE_URL=http://localhost:8080 \
//   USER_PAIRS_FILE=./spark_pairs.tsv \
//     k6 run load_spark_create.js
//
// USER_PAIRS_FILE is a tab-separated file: from_user_id<TAB>to_user_id
// per line. The script alternates target_kind per request to exercise
// the photo / prompt / tune branches in the spark validation path.
//
// Audit H1: a spark is accepted only for someone the sender was shown — a
// card dealt to them by GET /v1/dating/pulse/today (inside 24 h, still
// visible), a daily pick, an open match, or someone whose spark they may see.
// Anything else is 404 CANDIDATE_UNAVAILABLE, so a pair file alone no longer
// works. DEAL_MODE picks how each spark gets a dealt target:
//
//   DEAL_MODE=deck (default)  each iteration first fetches the sender's deck,
//       as the app does (that fetch records the dealt cards; its latency is
//       the separate pulse_today_latency_ms metric). It sparks pair.to when
//       the deck holds them, otherwise a random card of that deck; an empty
//       deck (daily allowance spent, nobody nearby) skips the iteration and
//       counts spark_skipped_no_card. Only the first case exercises the
//       mutual-spark match path for the pairs you chose.
//   DEAL_MODE=predealt        sparks pair.to directly, with no deck fetch. Use
//       only with pairs already dealt within the last 24 h: run
//       GET /v1/dating/pulse/today as each from_user_id beforehand and keep
//       the pairs whose to_user_id came back in data[].candidate_id (or, on
//       a disposable test database only, insert the pairs into
//       dating_deck_served (user_id, candidate_id, served_at = now())).
//       Pairs where to_user_id already sparked from_user_id (the match path)
//       need no deal at all.

import http from 'k6/http';
import { check, sleep } from 'k6';
import { SharedArray } from 'k6/data';
import { Trend, Rate, Counter } from 'k6/metrics';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const PAIRS_FILE = __ENV.USER_PAIRS_FILE || 'spark_pairs.tsv';
const DEAL_MODE = __ENV.DEAL_MODE || 'deck';
if (DEAL_MODE !== 'deck' && DEAL_MODE !== 'predealt') {
  throw new Error(`DEAL_MODE must be deck or predealt, got ${DEAL_MODE}`);
}

const pairs = new SharedArray('user_pairs', function () {
  const raw = open(PAIRS_FILE);
  return raw
    .split('\n')
    .map((line) => line.trim())
    .filter((line) => line.length > 0)
    .map((line) => {
      const [from, to] = line.split('\t');
      return { from, to };
    })
    .filter((p) => p.from && p.to);
});

const sparkLatency = new Trend('spark_create_latency_ms', true);
const sparkErrors = new Rate('spark_create_errors');
const matchesFormed = new Counter('spark_matches_formed_total');
const deckLatency = new Trend('pulse_today_latency_ms', true);
const skippedNoCard = new Counter('spark_skipped_no_card');

export const options = {
  scenarios: {
    spark_create: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '30s', target: 25 },
        { duration: '2m',  target: 50 },
        { duration: '2m',  target: 50 },
        { duration: '30s', target: 0  },
      ],
    },
  },
  thresholds: {
    spark_create_latency_ms: ['p(95)<350'],
    spark_create_errors:     ['rate<0.01'],
    http_req_failed:         ['rate<0.01'],
  },
};

const TARGET_KINDS = ['photo', 'prompt', 'tune_axis'];

// dealtTarget fetches the sender's deck (which deals its cards) and returns
// pair.to when it was dealt, else a random dealt card, else '' (no card).
function dealtTarget(pair) {
  const res = http.get(`${BASE_URL}/v1/dating/pulse/today`, {
    headers: { 'X-User-ID': pair.from },
    tags: { endpoint: 'pulse_today' },
  });
  deckLatency.add(res.timings.duration);
  if (res.status !== 200) {
    return '';
  }
  let ids = [];
  try {
    const body = res.json();
    ids = ((body && body.data) || []).map((c) => c.candidate_id).filter((id) => !!id);
  } catch (_) {
    return '';
  }
  if (ids.includes(pair.to)) {
    return pair.to;
  }
  return ids.length > 0 ? ids[Math.floor(Math.random() * ids.length)] : '';
}

export default function () {
  if (pairs.length === 0) {
    throw new Error('USER_PAIRS_FILE returned no lines');
  }
  const pair = pairs[Math.floor(Math.random() * pairs.length)];
  const to = DEAL_MODE === 'deck' ? dealtTarget(pair) : pair.to;
  if (!to) {
    skippedNoCard.add(1);
    sleep(Math.random() * 0.4 + 0.1);
    return;
  }
  const kind = TARGET_KINDS[Math.floor(Math.random() * TARGET_KINDS.length)];
  const payload = JSON.stringify({
    to_user_id: to,
    target_kind: kind,
    target_ref: `${kind}-loadtest-${Math.random().toString(36).slice(2, 9)}`,
    note: 'k6 spark create',
  });
  const res = http.post(`${BASE_URL}/v1/dating/sparks`, payload, {
    headers: {
      'X-User-ID': pair.from,
      'Content-Type': 'application/json',
    },
    tags: { endpoint: 'spark_create' },
  });
  sparkLatency.add(res.timings.duration);
  const ok = check(res, {
    '2xx': (r) => r.status >= 200 && r.status < 300,
  });
  if (!ok) {
    sparkErrors.add(1);
  } else {
    sparkErrors.add(0);
    try {
      const body = res.json();
      if (body && body.data && body.data.matched) {
        matchesFormed.add(1);
      }
    } catch (_) {
      // ignore — match-formed is best-effort telemetry.
    }
  }
  sleep(Math.random() * 0.4 + 0.1);
}

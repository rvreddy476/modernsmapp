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

import http from 'k6/http';
import { check, sleep } from 'k6';
import { SharedArray } from 'k6/data';
import { Trend, Rate, Counter } from 'k6/metrics';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const PAIRS_FILE = __ENV.USER_PAIRS_FILE || 'spark_pairs.tsv';

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

const TARGET_KINDS = ['photo', 'prompt', 'tune'];

export default function () {
  if (pairs.length === 0) {
    throw new Error('USER_PAIRS_FILE returned no lines');
  }
  const pair = pairs[Math.floor(Math.random() * pairs.length)];
  const kind = TARGET_KINDS[Math.floor(Math.random() * TARGET_KINDS.length)];
  const payload = JSON.stringify({
    to_user_id: pair.to,
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
      if (body && body.data && body.data.match_formed) {
        matchesFormed.add(1);
      }
    } catch (_) {
      // ignore — match-formed is best-effort telemetry.
    }
  }
  sleep(Math.random() * 0.4 + 0.1);
}

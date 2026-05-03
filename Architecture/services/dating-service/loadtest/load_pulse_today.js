// k6 — load test: GET /v1/dating/pulse/today
//
// Goal: 100 concurrent users, ramp to 500. P95 < 200ms target.
//
// Run:
//   BASE_URL=http://localhost:8080 \
//   USER_IDS_FILE=./load_users.txt \
//     k6 run load_pulse_today.js
//
// Pre-req: a CSV / line-separated file of test user UUIDs at the path
// pointed to by USER_IDS_FILE. Each VU pulls one row at random for the
// X-User-ID header. The auth interceptor in api-gateway is bypassed in
// staging via the dev `X-User-ID` short-circuit.

import http from 'k6/http';
import { check, sleep } from 'k6';
import { SharedArray } from 'k6/data';
import { Trend, Rate } from 'k6/metrics';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const USER_IDS_FILE = __ENV.USER_IDS_FILE || 'load_users.txt';

const userIds = new SharedArray('user_ids', function () {
  const raw = open(USER_IDS_FILE);
  return raw
    .split('\n')
    .map((s) => s.trim())
    .filter((s) => s.length > 0);
});

const pulseTodayLatency = new Trend('pulse_today_latency_ms', true);
const pulseTodayErrors = new Rate('pulse_today_errors');

export const options = {
  scenarios: {
    pulse_today_ramp: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '30s', target: 100 },
        { duration: '2m',  target: 100 },
        { duration: '1m',  target: 500 },
        { duration: '2m',  target: 500 },
        { duration: '30s', target: 0   },
      ],
      gracefulRampDown: '30s',
    },
  },
  thresholds: {
    pulse_today_latency_ms: ['p(95)<200'],
    pulse_today_errors:     ['rate<0.01'],
    http_req_failed:        ['rate<0.01'],
    http_req_duration:      ['p(95)<200'],
  },
};

export default function () {
  if (userIds.length === 0) {
    throw new Error('USER_IDS_FILE returned no lines');
  }
  const userId = userIds[Math.floor(Math.random() * userIds.length)];
  const res = http.get(`${BASE_URL}/v1/dating/pulse/today`, {
    headers: {
      'X-User-ID': userId,
      'Accept': 'application/json',
    },
    tags: { endpoint: 'pulse_today' },
  });
  pulseTodayLatency.add(res.timings.duration);
  const ok = check(res, {
    '200 OK': (r) => r.status === 200,
    'has data array': (r) => {
      try {
        const body = r.json();
        return body && Array.isArray(body.data);
      } catch (_) {
        return false;
      }
    },
  });
  if (!ok) {
    pulseTodayErrors.add(1);
  } else {
    pulseTodayErrors.add(0);
  }
  // Light think-time to mirror realistic open-the-app cadence.
  sleep(Math.random() * 0.5 + 0.1);
}

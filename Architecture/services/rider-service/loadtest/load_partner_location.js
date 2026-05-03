// k6 — load test: POST /v1/rider/partners/me/location
//
// Goal: this is the highest-traffic endpoint in rider-service. Every
// online partner pushes location every 5s; at scale that is thousands
// of writes/sec. We ramp 200 → 1000 partners pushing every 5s and verify
// p95 stays below 100ms with error rate < 0.5%.
//
// Stage shape:
//   30s warm-up to 200 VUs → 1m at 200 → 30s ramp to 1000 → 2m at 1000
//   → 30s ramp down.
//
// Pre-req: a tab-separated file `PARTNER_FILE` of one partner per line:
//   partner_id<TAB>seed_lat<TAB>seed_lng
// We jitter the lat/lng a little each iteration to mimic real movement
// without driving the lat/lng off-grid.
//
// Run:
//   BASE_URL=http://localhost:8080 \
//   INTERNAL_KEY=<rider-internal-key> \
//   TEST_USER_ID=<placeholder-uuid> \
//   PARTNER_FILE=./partner_seed.tsv \
//     k6 run load_partner_location.js

import http from 'k6/http';
import { check, sleep } from 'k6';
import { SharedArray } from 'k6/data';
import { Trend, Rate } from 'k6/metrics';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const INTERNAL_KEY = __ENV.INTERNAL_KEY || '';
const TEST_USER_ID = __ENV.TEST_USER_ID || '';
const PARTNER_FILE = __ENV.PARTNER_FILE || 'partner_seed.tsv';

const partners = new SharedArray('partners', function () {
  const raw = open(PARTNER_FILE);
  return raw
    .split('\n')
    .map((s) => s.trim())
    .filter((s) => s.length > 0)
    .map((line) => {
      const [partnerId, seedLat, seedLng] = line.split('\t');
      return {
        partnerId,
        seedLat: parseFloat(seedLat),
        seedLng: parseFloat(seedLng),
      };
    })
    .filter(
      (p) =>
        p.partnerId &&
        Number.isFinite(p.seedLat) &&
        Number.isFinite(p.seedLng),
    );
});

const locLatency = new Trend('rider_location_ingest_latency_ms', true);
const locErrors = new Rate('rider_location_ingest_errors');

export const options = {
  scenarios: {
    location_flood: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '30s', target: 200 },
        { duration: '1m',  target: 200 },
        { duration: '30s', target: 1000 },
        { duration: '2m',  target: 1000 },
        { duration: '30s', target: 0 },
      ],
      gracefulRampDown: '30s',
    },
  },
  thresholds: {
    rider_location_ingest_latency_ms: ['p(95)<100'],
    rider_location_ingest_errors:     ['rate<0.005'],
    http_req_failed:                  ['rate<0.005'],
    http_req_duration:                ['p(95)<100'],
  },
  summaryTrendStats: ['min', 'avg', 'med', 'max', 'p(90)', 'p(95)', 'p(99)'],
};

export default function () {
  if (partners.length === 0) {
    throw new Error('PARTNER_FILE returned no usable lines');
  }
  const p = partners[Math.floor(Math.random() * partners.length)];
  // Jitter ~50m on each axis, mimics in-traffic crawl.
  const dLat = (Math.random() - 0.5) * 0.001;
  const dLng = (Math.random() - 0.5) * 0.001;
  const speed = Math.random() * 50; // 0–50 km/h
  const heading = Math.random() * 360;
  const payload = JSON.stringify({
    lat: p.seedLat + dLat,
    lng: p.seedLng + dLng,
    speed: speed,
    heading: heading,
  });
  const headers = {
    'Content-Type': 'application/json',
    'X-Partner-ID': p.partnerId,
  };
  if (TEST_USER_ID) headers['X-User-ID'] = TEST_USER_ID;
  if (INTERNAL_KEY) headers['X-Internal-Key'] = INTERNAL_KEY;

  const res = http.post(
    `${BASE_URL}/v1/rider/partners/me/location`,
    payload,
    {
      headers,
      tags: { endpoint: 'rider_partner_location' },
    },
  );
  locLatency.add(res.timings.duration);
  const ok = check(res, {
    '2xx': (r) => r.status >= 200 && r.status < 300,
  });
  locErrors.add(ok ? 0 : 1);

  // Match real partner cadence: ~1 push every 5 seconds.
  sleep(5);
}

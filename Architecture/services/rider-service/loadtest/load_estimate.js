// k6 — load test: POST /v1/rider/estimate
//
// Goal: ramp 50 → 200 → 500 VUs over 6 minutes. Fare estimate is a hot
// pre-booking call — every customer hits it before they tap "Book ride",
// often multiple times as they tweak vehicle type. p95 < 200ms.
//
// Run:
//   BASE_URL=http://localhost:8080 \
//   INTERNAL_KEY=<rider-internal-key> \
//   TEST_USER_ID=<uuid> \
//   ESTIMATE_FILE=./estimate_pairs.tsv \
//     k6 run load_estimate.js
//
// ESTIMATE_FILE is tab-separated:
//   pickup_lat<TAB>pickup_lng<TAB>drop_lat<TAB>drop_lng<TAB>city_id

import http from 'k6/http';
import { check, sleep } from 'k6';
import { SharedArray } from 'k6/data';
import { Trend, Rate } from 'k6/metrics';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const INTERNAL_KEY = __ENV.INTERNAL_KEY || '';
const TEST_USER_ID = __ENV.TEST_USER_ID || '';
const ESTIMATE_FILE = __ENV.ESTIMATE_FILE || 'estimate_pairs.tsv';

const pairs = new SharedArray('estimate_pairs', function () {
  const raw = open(ESTIMATE_FILE);
  return raw
    .split('\n')
    .map((s) => s.trim())
    .filter((s) => s.length > 0)
    .map((line) => {
      const [pLat, pLng, dLat, dLng, cityId] = line.split('\t');
      return {
        pickupLat: parseFloat(pLat),
        pickupLng: parseFloat(pLng),
        dropLat: parseFloat(dLat),
        dropLng: parseFloat(dLng),
        cityId,
      };
    })
    .filter(
      (p) =>
        Number.isFinite(p.pickupLat) &&
        Number.isFinite(p.pickupLng) &&
        Number.isFinite(p.dropLat) &&
        Number.isFinite(p.dropLng) &&
        p.cityId,
    );
});

const estimateLatency = new Trend('rider_estimate_latency_ms', true);
const estimateErrors = new Rate('rider_estimate_errors');

const VEHICLE_TYPES = ['bike', 'auto', 'mini_cab', 'sedan', 'suv'];

export const options = {
  scenarios: {
    estimate_ramp: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '30s', target: 50 },
        { duration: '1m',  target: 50 },
        { duration: '30s', target: 200 },
        { duration: '1m30s', target: 200 },
        { duration: '30s', target: 500 },
        { duration: '1m',  target: 500 },
        { duration: '30s', target: 0 },
      ],
      gracefulRampDown: '30s',
    },
  },
  thresholds: {
    rider_estimate_latency_ms: ['p(95)<200'],
    rider_estimate_errors:     ['rate<0.01'],
    http_req_failed:           ['rate<0.01'],
    http_req_duration:         ['p(95)<200'],
  },
  summaryTrendStats: ['min', 'avg', 'med', 'max', 'p(90)', 'p(95)', 'p(99)'],
};

export default function () {
  if (pairs.length === 0) {
    throw new Error('ESTIMATE_FILE returned no usable lines');
  }
  const pair = pairs[Math.floor(Math.random() * pairs.length)];
  const vehicle = VEHICLE_TYPES[Math.floor(Math.random() * VEHICLE_TYPES.length)];
  const payload = JSON.stringify({
    pickup_lat: pair.pickupLat,
    pickup_lng: pair.pickupLng,
    drop_lat: pair.dropLat,
    drop_lng: pair.dropLng,
    vehicle_type: vehicle,
    city_id: pair.cityId,
  });
  const headers = {
    'Content-Type': 'application/json',
    'Accept': 'application/json',
  };
  if (TEST_USER_ID) headers['X-User-ID'] = TEST_USER_ID;
  if (INTERNAL_KEY) headers['X-Internal-Key'] = INTERNAL_KEY;

  const res = http.post(`${BASE_URL}/v1/rider/estimate`, payload, {
    headers,
    tags: { endpoint: 'rider_estimate' },
  });
  estimateLatency.add(res.timings.duration);
  const ok = check(res, {
    '2xx': (r) => r.status >= 200 && r.status < 300,
    'has fare_estimate_paise': (r) => {
      try {
        const body = r.json();
        const data = (body && body.data) || body || {};
        return typeof data.fare_estimate_paise === 'number';
      } catch (_) {
        return false;
      }
    },
  });
  estimateErrors.add(ok ? 0 : 1);
  // Real users tweak inputs; cadence ~1 estimate every 1.5–3s.
  sleep(Math.random() * 1.5 + 1.0);
}

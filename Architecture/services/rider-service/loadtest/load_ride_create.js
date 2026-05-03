// k6 — load test: POST /v1/rider/rides
//
// Goal: 50 concurrent customers booking rides every 10–30 seconds. Ride
// creation includes the synchronous matcher dispatch (offer fan-out to
// candidate partners), so end-to-end p95 includes Redis GEORADIUS, the
// matcher scoring loop, and a Postgres write.
//
// Pre-req: at least 10 mock partners online in the target city via the
// `/v1/rider/partners/me/online` + `/v1/rider/partners/me/location`
// endpoints (use `load_partner_location.js` in a side-car run).
//
// Thresholds:
//   * p95 < 500ms (booking includes match)
//   * error rate < 1%
//   * time-to-match P95 < 30s with >= 10 mock partners online
//
// Run:
//   BASE_URL=http://localhost:8080 \
//   INTERNAL_KEY=<rider-internal-key> \
//   TEST_USER_ID=<customer-uuid> \
//   RIDE_FILE=./ride_pairs.tsv \
//     k6 run load_ride_create.js

import http from 'k6/http';
import { check, sleep } from 'k6';
import { SharedArray } from 'k6/data';
import { Trend, Rate, Counter } from 'k6/metrics';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const INTERNAL_KEY = __ENV.INTERNAL_KEY || '';
const TEST_USER_ID = __ENV.TEST_USER_ID || '';
const RIDE_FILE = __ENV.RIDE_FILE || 'ride_pairs.tsv';

const pairs = new SharedArray('ride_pairs', function () {
  const raw = open(RIDE_FILE);
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
    });
});

const rideLatency = new Trend('rider_ride_create_latency_ms', true);
const rideErrors = new Rate('rider_ride_create_errors');
const matchLatency = new Trend('rider_time_to_match_ms', true);
const matchedRides = new Counter('rider_rides_matched_total');

const VEHICLES = ['auto', 'mini_cab', 'sedan'];
const PAYMENTS = ['wallet', 'cash', 'upi'];

export const options = {
  scenarios: {
    ride_create_steady: {
      executor: 'constant-vus',
      vus: 50,
      duration: '5m',
    },
  },
  thresholds: {
    rider_ride_create_latency_ms: ['p(95)<500'],
    rider_ride_create_errors:     ['rate<0.01'],
    rider_time_to_match_ms:       ['p(95)<30000'],
    http_req_failed:              ['rate<0.01'],
  },
  summaryTrendStats: ['min', 'avg', 'med', 'max', 'p(90)', 'p(95)', 'p(99)'],
};

function uuidv4() {
  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, (c) => {
    const r = (Math.random() * 16) | 0;
    const v = c === 'x' ? r : (r & 0x3) | 0x8;
    return v.toString(16);
  });
}

export default function () {
  if (pairs.length === 0) {
    throw new Error('RIDE_FILE returned no usable lines');
  }
  const pair = pairs[Math.floor(Math.random() * pairs.length)];
  const vehicle = VEHICLES[Math.floor(Math.random() * VEHICLES.length)];
  const payment = PAYMENTS[Math.floor(Math.random() * PAYMENTS.length)];
  const idem = uuidv4();
  const payload = JSON.stringify({
    pickup: { lat: pair.pickupLat, lng: pair.pickupLng },
    drop: { lat: pair.dropLat, lng: pair.dropLng },
    vehicle_type: vehicle,
    city_id: pair.cityId,
    payment_method: payment,
    idempotency_key: idem,
  });
  const headers = {
    'Content-Type': 'application/json',
    'Accept': 'application/json',
  };
  if (TEST_USER_ID) headers['X-User-ID'] = TEST_USER_ID;
  if (INTERNAL_KEY) headers['X-Internal-Key'] = INTERNAL_KEY;

  const t0 = Date.now();
  const res = http.post(`${BASE_URL}/v1/rider/rides`, payload, {
    headers,
    tags: { endpoint: 'rider_ride_create' },
  });
  rideLatency.add(res.timings.duration);
  const created = check(res, {
    '2xx': (r) => r.status >= 200 && r.status < 300,
  });
  rideErrors.add(created ? 0 : 1);

  if (!created) {
    sleep(Math.random() * 20 + 10);
    return;
  }

  let rideId = null;
  try {
    const body = res.json();
    const data = (body && body.data) || body || {};
    rideId = data.id || data.ride_id || null;
  } catch (_) {
    // ignore — body was not JSON
  }

  // Poll for match: time-to-match is the gap between create and the first
  // accepted offer (status transitions to "accepted").
  if (rideId) {
    const deadline = Date.now() + 60000; // 60s upper bound for poll
    while (Date.now() < deadline) {
      sleep(2);
      const poll = http.get(`${BASE_URL}/v1/rider/rides/${rideId}`, {
        headers,
        tags: { endpoint: 'rider_ride_get_poll' },
      });
      if (poll.status !== 200) continue;
      let status = '';
      try {
        const b = poll.json();
        status = ((b && b.data) || b || {}).status || '';
      } catch (_) {
        status = '';
      }
      if (status === 'accepted' || status === 'arriving' ||
          status === 'arrived' || status === 'in_progress' ||
          status === 'completed') {
        matchLatency.add(Date.now() - t0);
        matchedRides.add(1);
        break;
      }
      if (status === 'cancelled' || status === 'expired') break;
    }
  }

  // Customer cadence between bookings.
  sleep(Math.random() * 20 + 10);
}

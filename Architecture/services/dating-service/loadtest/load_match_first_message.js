// k6 — load test: internal first-message-on-match throughput.
//
// Goal: simulate message-service throughput on the "first message on a
// fresh Pulse match" hot path. We hit the dating-service internal layer-1
// moderation scan endpoint (called by message-service for every dating-
// context message) plus the dating-service match-existence check that
// gates the pre-message banner.
//
// Targets: p95 < 200ms, error rate < 1%.
//
// Run:
//   BASE_URL=http://localhost:8080 \
//   INTERNAL_URL=http://localhost:8112 \
//   MATCH_PAIRS_FILE=./match_pairs.tsv \
//     k6 run load_match_first_message.js
//
// MATCH_PAIRS_FILE is tab-separated:
//   match_id<TAB>conversation_id<TAB>sender_id

import http from 'k6/http';
import { check, sleep } from 'k6';
import { SharedArray } from 'k6/data';
import { Trend, Rate } from 'k6/metrics';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const INTERNAL_URL = __ENV.INTERNAL_URL || 'http://localhost:8112';
const PAIRS_FILE = __ENV.MATCH_PAIRS_FILE || 'match_pairs.tsv';

const matches = new SharedArray('match_pairs', function () {
  const raw = open(PAIRS_FILE);
  return raw
    .split('\n')
    .map((s) => s.trim())
    .filter((s) => s.length > 0)
    .map((line) => {
      const [matchId, conversationId, senderId] = line.split('\t');
      return { matchId, conversationId, senderId };
    })
    .filter((m) => m.matchId && m.conversationId && m.senderId);
});

const scanLatency = new Trend('moderation_scan_latency_ms', true);
const scanErrors = new Rate('moderation_scan_errors');
const matchLatency = new Trend('match_lookup_latency_ms', true);
const matchErrors = new Rate('match_lookup_errors');

export const options = {
  scenarios: {
    first_message_burst: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '30s', target: 50 },
        { duration: '2m',  target: 150 },
        { duration: '2m',  target: 150 },
        { duration: '30s', target: 0   },
      ],
    },
  },
  thresholds: {
    moderation_scan_latency_ms: ['p(95)<200'],
    moderation_scan_errors:     ['rate<0.01'],
    match_lookup_latency_ms:    ['p(95)<200'],
    match_lookup_errors:        ['rate<0.01'],
    http_req_failed:            ['rate<0.01'],
  },
};

const SAMPLE_BODIES = [
  'hey, how was your weekend?',
  'loved your reel about chai stalls',
  'we both picked the same prompt :)',
  'are you free for coffee saturday?',
  'long-time tabla player here too!',
];

export default function () {
  if (matches.length === 0) {
    throw new Error('MATCH_PAIRS_FILE returned no lines');
  }
  const pair = matches[Math.floor(Math.random() * matches.length)];
  const body = SAMPLE_BODIES[Math.floor(Math.random() * SAMPLE_BODIES.length)];

  // 1. Match-existence check (banner gate).
  const matchRes = http.get(
    `${BASE_URL}/v1/dating/matches/${pair.matchId}`,
    {
      headers: {
        'X-User-ID': pair.senderId,
        'Accept': 'application/json',
      },
      tags: { endpoint: 'match_lookup' },
    }
  );
  matchLatency.add(matchRes.timings.duration);
  const matchOk = check(matchRes, {
    'match 2xx': (r) => r.status >= 200 && r.status < 300,
  });
  matchErrors.add(matchOk ? 0 : 1);

  // 2. Internal layer-1 scan — message-service calls dating-service direct.
  const scanPayload = JSON.stringify({
    message_id: '00000000-0000-0000-0000-' +
      Math.floor(Math.random() * 1e12).toString(16).padStart(12, '0'),
    conversation_id: pair.conversationId,
    sender_id: pair.senderId,
    body: body,
  });
  const scanRes = http.post(
    `${INTERNAL_URL}/internal/v1/dating/moderation/scan-layer1`,
    scanPayload,
    {
      headers: { 'Content-Type': 'application/json' },
      tags: { endpoint: 'moderation_scan_layer1' },
    }
  );
  scanLatency.add(scanRes.timings.duration);
  const scanOk = check(scanRes, {
    'scan 2xx': (r) => r.status >= 200 && r.status < 300,
    'has action_taken': (r) => {
      try {
        const b = r.json();
        return b && b.data && typeof b.data.action_taken === 'string';
      } catch (_) {
        return false;
      }
    },
  });
  scanErrors.add(scanOk ? 0 : 1);

  sleep(Math.random() * 0.3 + 0.1);
}

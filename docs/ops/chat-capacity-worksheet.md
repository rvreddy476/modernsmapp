# Chat capacity worksheet — atPost/VChat

Date: 2026-08-25. Status: MODEL ONLY — no load evidence exists yet. Nothing
here claims proven capacity; the staged AWS load gate below is what converts
each row from assumption to evidence (directive §6, CH-LB-8).

## 1. Input assumptions (replace with real numbers per stage)

| Variable | Stage A (beta) | Stage B | Stage C |
|---|---|---|---|
| Registered users | 100 k | 1 M | 10 M |
| DAU | 15 k | 150 k | 1.5 M |
| Peak concurrent sockets (≈35% of DAU) | 5 k | 50 k | 500 k |
| Messages / active user / day | 40 | 40 | 40 |
| Peak message rate (5× mean) | ~35/s | ~350/s | ~3,500/s |
| Avg envelope size (text + frame) | 1 KB | 1 KB | 1 KB |
| Attachment share / avg size | 8% / 800 KB | 8% / 800 KB | 8% / 800 KB |
| Group size p50 / p99 / cap | 4 / 60 / 1,024 | same | same |
| Retention (hot Scylla) | 12 mo | 12 mo | tiered |

## 2. Derived load (Stage C, peak)

- **Send path** (HTTP → PG intent → Scylla write → inbox projection → outbox):
  ~3.5 k writes/s to Scylla `messages` + fan-out `conversations_by_user`
  writes ≈ 3.5 k × avg-members(≈5) ≈ 17.5 k upserts/s — partition-local,
  well inside a 6–9 node NVMe Scylla ring; hot-partition risk concentrates in
  max-size groups (1,024 inbox upserts per message) and celebrity accounts —
  the required hot-key test (CH-LB-8.4).
- **Redis pub/sub**: today one PUBLISH per member per message + one room
  publish. At Stage C worst case (p99 group 60) ≈ 210 k publishes/s burst —
  the number that makes the entitled-room fan-out the REQUIRED path beyond
  Stage B: room publish is 1/message + per-member only for inbox pings.
  Recorded as the scaling cliff; the foundation shipped this pass.
- **Sockets**: 500 k concurrent at ~20 KB/conn ≈ 10 GB fleet memory → 10–17
  gateway pods (30–50 k conns each) behind NLB; slow-consumer disconnect is
  already bounded (256-frame buffer).
- **PG (chat schema)**: conversations/membership/read-cursor upserts ≈ 8 k
  writes/s Stage C; read cursors are the hottest new table — PK upserts,
  monotonic guard, no scans. Partitioning by conversation_id hash is the
  Stage C lever.
- **Kafka**: chat.events.v1 ≈ message rate + membership/receipt events ≈
  5 k msgs/s Stage C — modest; partition by conversation_id to preserve
  per-conversation order (never one global partition).

## 3. Scaling levers (in order)

1. Gateway horizontal scale (stateless; Redis cluster pub/sub sharding).
2. Entitled room fan-out replacing per-member publish for groups > ~8.
3. Scylla ring growth + inbox projection TTL/compaction tuning.
4. PG read-cursor partitioning; policy projection is per-user PK (no lever needed).
5. Regional cells at Stage C+ (per-region Redis + gateway, global Kafka).

## 4. Staged AWS load gate (repeatable; each stage must pass WITH AZ loss)

For each stage: (1) soak at mean rate 2 h; (2) burst at 5× for 15 min;
(3) kill one AZ's pods + one Redis replica mid-burst; (4) verify: no message
loss (HTTP reconciliation closes gaps), duplicate rate 0 at clients,
p99 send < 400 ms, socket reconnect storm drains < 2 min, consumer lag
returns to 0 < 5 min. Tooling: k6/ghz senders + a socket herd driver;
scripts to live under `Architecture/load/chat/` (NOT yet written — a named
gap in the handover).

## 5. Alarm thresholds (initial; tune per stage)

active_sockets vs capacity 80%; connect/auth failure > 1%/5 min; outbound
queue saturation disconnects > 10/min/pod; HTTP send p99 > 500 ms; delivery
intents pending > 60 s age; outbox unpublished age > 30 s; Kafka lag > 10 k;
privacy fail-closed events > 100/5 min (dependency outage signal);
policy-projection fetch failures > 1%.

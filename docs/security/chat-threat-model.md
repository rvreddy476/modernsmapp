# Chat threat model — atPost/VChat private messaging

Date: 2026-08-25 · Scope: the production-chat pass (direct, requests, groups,
realtime, offline client, chat lock). E2EE is NOT deployed (see the E2EE ADR);
every claim below is made for the transport-encrypted system that exists.

## 0. Honest summary — what the system protects today, and what it cannot

Protects: transport confidentiality (TLS), authenticated access (JWT + graph
policy), privacy-gated creation/disclosure (server-enforced, fail-closed),
durable delivery without duplication (idempotency + delivery intents), local
chat privacy on a shared/stolen device (chat lock), request-spam damping.

Cannot protect (until E2EE ships): message plaintext from a compromised or
legally compelled backend, database snapshot, Kafka topic, or log-adjacent
operator with database access. Message text is stored server-side in Scylla,
the PG last-message preview, and the Scylla inbox projection — by design, and
disclosed as such. NO "zero knowledge" claim is made anywhere.

## 1. Malicious/compromised backend or delivery service

- TODAY: full plaintext access — the top residual risk; mitigation is the
  E2EE ADR's launch-blocker path plus least-privilege access, structured
  no-body logging, and at-rest encryption as defence in depth.
- ws-gateway cannot be steered onto arbitrary rooms by clients: personal
  channels are derived from the authenticated JWT; conversation rooms need
  an owner-issued entitlement (chat-shared/roomauth) that the gateway only
  verifies, never mints.

## 2. Stolen device

- Locked (chat lock enabled): chat surfaces do not compose while locked —
  message text is absent from the UI tree/semantics/task-switcher snapshot of
  the gate. PIN attempts are throttled (5 tries, doubling lockout) with the
  throttle check before the KDF (no timing distinction). Verifier is
  PBKDF2-HMAC-SHA256 (310k, random salt) — upgrade to a memory-hard KDF
  (argon2id via vetted lib) is pre-launch hardening debt.
- Unlocked stolen device: full access to this account's chat, as with any
  messenger; remote session revocation via the identity platform is the
  mitigation.
- Local Room cache holds plaintext in app-private storage (standard app
  sandbox). Full-filesystem-extraction adversaries (root/forensic) read it;
  moving the cache under a Keystore-wrapped key is listed hardening debt and
  becomes mandatory with E2EE.

## 3. Malicious group member / removed member

- Any member can exfiltrate content they can legitimately read (screenshot,
  copy) — unfixable by cryptography; stated in UI norms, mitigated by report
  flows.
- Removed member: membership rows are severed (left_at) inside the removal
  transaction; CheckMembership fails from that statement on for history,
  send and entitlement issuance. Connected sockets receive
  subscription_revoked immediately; the entitlement expiry (5 min) bounds a
  lost control frame. With E2EE, epoch rotation on removal is a CH-LB-5 gate.
- Blocked pairs can never be newly co-added (graph facts deny). Two existing
  members who block each other retain the common group's visibility —
  disclosed in product copy, not silently corrupted.

## 4. Replay / reorder / duplicate delivery

- Sends: server-side idempotency keys + delivery intents make retries replay
  the identical message identity; the Android outbox retries under the SAME
  key across process death.
- Receives: client de-duplicates by server message id at BOTH the in-memory
  thread and the Room primary key; socket, HTTP, push and retry copies
  collapse to one row.
- Entitlements carry version, audience, expiry, nonce and subject; signature
  is HMAC-SHA256 with constant-time comparison.

## 5. MITM / key substitution

- TODAY: TLS + pinned gateway host; no user-visible key verification exists
  because no user keys exist. Safety-number verification is a CH-LB-5
  criterion for the E2EE phase.

## 6. Spam / scraping / request flooding / invite abuse

- Message requests: 20/24h per sender, one link-free ≤500-char introduction,
  decline cooldown (7 days) that survives idempotency-key rotation, block
  and report as idempotent severing transitions.
- Group adds obey the target's policy server-side; consent-required targets
  get invitations; denial and dependency failure both fail closed.
- Second-degree checks are answered as one boolean by graph-service —
  adjacency lists never reach a client.
- DM sends: 60/60s per user; ws frames capped at 10 KB.

## 7. Push provider compromise / notification leakage

- Push payloads carry opaque ids; `show_message_preview=false` (and, later,
  E2EE conversations by default) render generic text. Device-side, chat-lock
  redaction of previews while locked is wired at the UI gate; the
  notification-service preview path must be re-verified when push preview
  templates change (tracked).

## 8. Logs / analytics / backups / crash reports

- Chat services log request ids, opaque ids, outcomes and latency — the
  logging guidance in the directive §6 is codified in the handover; message
  bodies, PINs, tokens, entitlements and preview text are prohibited log
  fields. The local crash reporter writes uncaught traces only.

## 9. Rooted devices / screen capture

- Out of scope for protection; the lock reduces casual exposure only. No
  FLAG_SECURE is currently set on chat screens (listed as cheap hardening).

## 10. Account takeover / device linking / recovery

- Sessions are the identity platform's domain (2FA, session list, revocation
  already shipped there). Chat-specific: the ws token-expiry watchdog closes
  sockets when a JWT dies; logout tears down socket, outbox, cache and lock.
- Forgotten chat PIN: reset wipes local chat material (no backdoor);
  conversations re-sync from the server after re-auth.

## 11. Legal/abuse reporting under E2EE (forward-looking)

- Reporting a message request uploads the SERVER-STORED preview (plaintext
  system). Under E2EE, reporting will require explicit user consent to
  decrypt-and-submit the excerpt with integrity context — the directive's
  model, recorded here so the CH-LB-5 build implements it rather than a
  server-side read.

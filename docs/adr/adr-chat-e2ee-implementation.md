# ADR — E2EE implementation source for atPost/VChat private messaging

Date: 2026-08-25 · Status: **DECISION REQUIRED (founder + legal + security)** ·
Author: production-chat implementation pass (directive §4).

Time-box honoured: this is the capped half-day source/licence/build
feasibility evaluation. **No cryptography was written or shipped in this
pass.** Every chat surface remains, and is labelled as, standard
transport-encrypted (TLS + at-rest) chat. The phrase "end-to-end encrypted",
lock badges and "user-owned key" language remain FORBIDDEN in every product
surface until the checklist in directive §3.7 passes (CH-LB-5).

## 1. The bar

A candidate must provide, per directive §3.7/§4: endpoint-generated keys,
server sees only public key packages + opaque ciphertext, forward secrecy AND
post-compromise recovery from a standard reviewed protocol, per-device
identity/revocation, safety-number verification, member-change epoch rotation,
client-side attachment encryption, replay/reorder tolerance, and identical
versioned wire behaviour from Kotlin and (future) Swift. Licence must permit
closed-source commercial distribution without contaminating the app.

## 2. Candidates

### 2.1 Matrix Rust SDK crypto (`matrix-sdk-crypto` + vodozemac) — RECOMMENDED CANDIDATE

- **Licence:** Apache-2.0 (verified on the repository, 2026-08-25) —
  compatible with closed-source distribution.
- **Shape:** the project documents `matrix-sdk-crypto` as "a standalone
  encryption state machine with no network I/O" (verified 2026-08-25) —
  exactly the integration contract our message service needs: WE keep the
  chat product/protocol and route versioned opaque envelopes; the state
  machine handles Olm (pairwise) + Megolm (group) sessions, device tracking,
  verification (SAS/QR) and key backup primitives.
- **Bindings:** the repo states the higher-level crates embed in Swift and
  Kotlin. The dedicated crypto FFI artifacts (`matrix-sdk-crypto-ffi`,
  published Android AAR / Swift package used by Element X) were NOT verified
  from a primary source inside the time-box — recorded as verification item
  V1 below.
- **Audits:** vodozemac (the Olm/Megolm Rust implementation underneath) has a
  published Least Authority audit (2022). NOT re-verified from a primary
  source inside the time-box — verification item V2.
- **Protocol properties:** Olm gives double-ratchet forward secrecy + PCS for
  pairwise channels. Megolm group sessions give forward secrecy via ratchet
  and handle membership by re-keying on membership change; its
  post-compromise recovery is coarser than MLS (rotation-based, not
  per-message tree updates) — an accepted, widely deployed trade
  (Matrix/Element production) that must be stated honestly in the threat
  model if adopted.
- **Fit risks:** identity/device model is Matrix-shaped (user id + device id
  + cross-signing); mapping onto our identity platform needs a compatibility
  spike (V3). Binary size and JNI overhead unmeasured (V4).

### 2.2 OpenMLS (RFC 9420 MLS)

- **Licence:** MIT (verified 2026-08-25). **Standard:** implements RFC 9420
  as stated by the project (verified 2026-08-25) — the strongest group-chat
  protocol properties on the list (tree-based PCS, efficient large-group
  commits: our 1,024-member cap is squarely MLS territory).
- **Gaps:** no OFFICIAL Android/Swift bindings verified (build targets for
  aarch64-android/ios exist; the FFI layer, delivery-service integration
  ("MLS needs a Delivery Service component we would have to build"), and an
  independent audit were not verifiable from the repository page inside the
  time-box. Higher integration cost: we would own binding + DS + storage
  layers ourselves.
- **Position:** the right LONG-TERM protocol; the highest engineering-risk
  path for a first E2EE launch.

### 2.3 libsignal

- **Licence:** AGPLv3. **Blocking** for our closed-source distribution
  unless legal explicitly accepts AGPL obligations or negotiates terms
  (directive §4.4 conditions use on explicit legal acceptance). Signal also
  historically discourages third-party use of its infrastructure code.
- **Position:** excluded unless legal actively chooses it.

### 2.4 Audited commercial SDK

- Candidates exist (e.g. E2EE SDK vendors with Android+Swift support). None
  evaluated to the evidence bar inside the time-box; a vendor selection would
  need: exportable key state (no lock-in), no vendor-held plaintext keys,
  audit reports, and licence cost at projected MAU. Open per V5.

## 3. Decision requested

Adopt **matrix-sdk-crypto** as the E2EE engine behind the platform-neutral
contracts below, CONDITIONAL on closing V1–V4; keep OpenMLS as the tracked
successor for group-scale properties. Until the conditions close and CH-LB-5
passes end-to-end, **E2EE remains a named launch blocker** and no UI may
claim it.

Verification items (next exact actions):
- **V1**: confirm maintained Android/Swift crypto FFI artifacts from primary
  sources; pin versions.
- **V2**: obtain and file the vodozemac audit report; check CVE history.
- **V3**: one-week spike — two devices exchanging Olm/Megolm ciphertext
  through OUR message-service envelopes (no Matrix homeserver), including
  offline key-claim, replay and device-revocation paths.
- **V4**: measure APK delta, cold-start and per-message latency on a low-end
  device.
- **V5**: legal review of Apache-2.0 NOTICE obligations (trivial) and a
  comparative commercial-SDK quote if the founder wants the buy option.

## 4. Integration contracts (already respected by this pass's code)

The message service routes versioned opaque envelopes and never depends on a
mobile crypto package. When a candidate is accepted it lands behind:
`CryptoIdentityStore`, `DeviceKeyPackageRepository`,
`ConversationCryptoSession`, `EncryptedMessageEnvelope`,
`EncryptedAttachmentEnvelope`, `RecoveryKeyStore`.

## 5. What is forbidden meanwhile

- Any "end-to-end encrypted" claim, lock badge, or "only you can read this"
  copy anywhere in the product.
- Reviving the archived Signal/X3DH-style stub or the placeholder Secret
  Chat UI as "evidence".
- Presenting TLS, SSE-KMS, disk encryption, the chat PIN, or a server-held
  AES helper as E2EE.

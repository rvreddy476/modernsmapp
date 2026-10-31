# atPost / US Super-App — Native iOS Architecture & Implementation Plan (v2)

> **Document Role**: Principal iOS Architect Specification
> **Target OS**: iOS 17.0+ · Swift 6 language mode · built with the Xcode/SDK currently required by App Store Connect (Xcode 26 / iOS 26 SDK at time of writing)
> **Application Type**: Modular native super-app, shipped in staged releases
> **Status**: **Draft v2 — pending the decisions in §16 before implementation starts**

---

## 0. Changes from v1

| Area | v1 | v2 |
|---|---|---|
| Scope | 18 verticals in 20 weeks | A **Release 1 MVP** (social, chat, calling) in ~26 weeks, with the other verticals in Releases 2–4 (§2, §12). Team size is stated as an assumption. |
| Calling stack | LiveKit Swift SDK + GoogleWebRTC | **LiveKit Swift SDK only.** It ships its own WebRTC build, and GoogleWebRTC is deprecated, CocoaPods-only and clashes on symbols. |
| E2EE | Custom Curve25519 / AES-GCM built on CryptoKit | An **audited protocol library** (MLS or Olm/Megolm), with license review. Moved to Release 2 (§7.6). |
| Payments | "Non-custodial wallet", Add Money, Apple Pay | A regulatory model decision (PPI licence or not), UPI through a licensed partner, Apple Pay removed (not available in India at time of writing) (§8). |
| Mini-apps | Listed in `DomainSuperApp`, never designed | First-party only until Release 4. The third-party model is bound by App Review 2.5.2 and 4.7 (§8.4). |
| Compliance | ATT + nutrition labels + Apple Pay | The full App Store and India regulatory checklist (§10). |
| Module tree | Tier 3A missing; roadmap modules missing from the tree | All tiers and modules shown, with written dependency rules that CI enforces (§4, §5). |
| Routing | Central `NavigationPath`, ownership unclear | One typed, `Codable` stack per tab, with destinations resolved in the composition root (§6). |
| VoIP / audio | Activate `playAndRecord` directly | The audio session is driven by CallKit's `didActivate`, and every VoIP push is reported to CallKit (§7.2). |
| Storage | One GRDB database, no cross-process design | Separate databases, App Group sharing rules, Data Protection classes (§7.4). |
| Testing / CI | `swift test` on iOS-only packages; iPhone 16 / iOS 18.0 | `xcodebuild test` per package on an iOS Simulator, selective testing, current toolchain (§13). |
| Missing sections | — | Added observability, feature flags, security, localization, accessibility, app size, background work, API contract, team, risks and open decisions. |
| Numbers | "<5 s", "~0.2 s", "<10 ms", locked 120 FPS | Replaced with measurable budgets that are baselined in Phase 1 and checked in CI (§1). |

---

## 1. Executive Summary & Architecture Principles

atPost combines social (feed, reels, stories, long-form PostTube), messaging and calling, the creator economy, hyperlocal services (FiGo food, Mopedu rides, Doorstep), payments (UPI, BBPS) and dating (Pulse) in one iOS app.

At this scope the main risk is product and delivery scope more than technology. The architecture therefore aims to:
- **ship a focused Release 1 well**, and
- **add verticals later without reworking the core.**

### Principles (each one comes with a measurable budget)

| # | Principle | Budget / check |
|---|---|---|
| 1 | **Features are isolated.** A feature module builds, previews and tests against interface modules and mocks only. It never imports another feature. | Dependency rules checked in CI (§4.2). Incremental "edit → preview refresh" budget set in Phase 1; working target is under 10 s on the standard dev machine. |
| 2 | **Media is smooth.** Reels and feed scroll without visible hitches. | Scroll hitch-time ratio **under 5 ms/s** on the reference device. Next-reel first frame **under 300 ms p90** on Wi-Fi. Footprint during continuous playback **under 200 MB**. |
| 3 | **Concurrency is safe.** | Swift 6 language mode with complete strict-concurrency checking in every package. Zero suppressed concurrency warnings without a linked ticket. |
| 4 | **Offline-first start.** The last cached content shows before any network call. | Time to first frame **under 400 ms p50** on the reference device (`XCTApplicationLaunchMetric`, MetricKit in production). Cached feed visible **under 1 s p90**. |
| 5 | **Deep OS integration.** CallKit/PushKit, Live Activities, App Intents, communication notifications, biometrics. | Each one has a platform-rule checklist (§7). |

> **Reference device**: one mid-tier supported iPhone (to be agreed, e.g. iPhone 12 or 13). All budgets are measured on it, not on the newest Pro hardware.

---

## 2. Scope & Release Plan

| Release | Contents | Notes |
|---|---|---|
| **R1 — MVP** (~26 weeks) | Auth & onboarding, app shell, Feed, Profile, Explore/Search, Reels, Stories, basic Create (photo/video post), Chat (1:1 and group, encrypted in transit), Voice/Video calling, notifications, Trust & Safety, compliance | Shows that the social and messaging core holds up at scale |
| **R2 — Hyperlocal & Payments** | Services Hub, FiGo, Mopedu, Live Activities, UPI payments through a partner, BBPS, **E2EE Secret Chats** | Gated on decisions D1–D3 (§16) |
| **R3 — Creator & Media** | PostTube (Watch), Creator Studio, Live streaming & gifting, Communities, Commerce/Marketplace | Monetisation needs in-app purchase (§10.1) |
| **R4 — Platform** | Third-party mini-app platform, Doorstep, Pulse (dating) | Pulse should ideally be a **separate app** (§10.1) |

**Team assumption (to confirm):** about 10 iOS engineers in three pods, plus QA, design and backend counterparts:
- **Platform** (3): Tiers 1–2, CI, performance.
- **Social & Media** (4): feed, reels, stories, create, profile.
- **Messaging & Calling** (3): realtime, chat, calling, notifications.

The timelines in §12 are only valid under this assumption.

---

## 3. Tech Stack

| Layer | Choice | Notes |
|---|---|---|
| Language & tooling | **Swift 6** (language mode 6), **XcodeGen + SPM** local packages | `Us.xcodeproj` is generated and gitignored. Evaluate **Tuist** if module count or CI time grows past budget, since it adds binary caching and selective testing. |
| UI | **SwiftUI** first, with **UIKit** bridges | Bridges for `AVPlayerLayer`, the Reels pager (§7.1), LiveKit video views, camera capture, and VisionKit scanning. |
| State | **MVVM with `@Observable`**, `@MainActor` view models, one-way data flow | A view model exposes read-only state and a `send(_ intent:)` method. Side effects run in domain services (actors/`Sendable`). No Combine in new code. |
| Navigation | One **typed `NavigationStack` per tab** with `[AppRoute]` paths | See §6. |
| Local storage | **GRDB.swift** (SQLite, WAL, FTS5) + **Keychain** | Cross-process rules in §7.4. If encryption beyond Data Protection is needed, confirm the SQLCipher integration path in the Phase 0 spike. |
| Crypto / keys | **CryptoKit**; Secure Enclave **P-256** keys for device-bound signing | The Secure Enclave doesn't support Curve25519. E2EE uses a protocol library (§7.6). |
| Networking | **`URLSession`** + async/await; client generated by **swift-openapi-generator** from the backend's OpenAPI spec | HTTP/3, background upload sessions, certificate pinning (§9). |
| Realtime | **`URLSessionWebSocketTask`** wrapped in an actor, plus SSE via `AsyncThrowingStream` | The socket is suspended in the background. Background delivery always comes from APNs/PushKit. Channel multiplexing is our own protocol on top. |
| Video | **AVFoundation** (`AVQueuePlayer`, `AVPlayerLooper`), HLS, Metal for filters | FairPlay-ready, Picture in Picture. |
| Calling | **LiveKit Swift SDK** (its bundled WebRTC) + **CallKit** + **PushKit** | Don't add GoogleWebRTC. |
| Push | APNs, PushKit (VoIP only), Notification Service Extension, communication notifications (`INSendMessageIntent`) | §7.5 |
| Scanning | **VisionKit `DataScannerViewController`** for UPI QR | Falls back to `AVCaptureMetadataOutput`. |
| Observability | A crash reporter (vendor in D7), **MetricKit**, `OSLog`/`os_signpost` | §11.1 |
| Testing | **Swift Testing** + XCTest, swift-snapshot-testing, XCUITest, `XCTMetric` | §13 |

---

## 4. Module Architecture

### 4.1 Tiers

```
┌──────────────────────────────────────────────────────────────────┐
│ TIER 5  APP COMPOSITION ROOT                                     │
│ UsApp · CompositionRoot · RouteResolver · RootTabView · Lifecycle│
└──────────────────────────────┬───────────────────────────────────┘
                               ▼
┌──────────────────────────────────────────────────────────────────┐
│ TIER 4  FEATURES  (Views, ViewModels)                            │
│ FeatureFeed · FeatureReels · FeatureChat · FeatureCalling · …    │
└───────────────┬──────────────────────────────┬───────────────────┘
                ▼                              ▼
┌───────────────────────────────┐ ┌────────────────────────────────┐
│ TIER 3A  INTERFACES           │ │ TIER 3B  DOMAIN                │
│ UsNavigation (AppRoute)       │ │ DomainAuth · DomainSocial ·    │
│ Feature<X>Interface: routes,  │ │ DomainChat · DomainCalling · … │
│ inputs/outputs, protocols     │ │ Repositories, services, actors │
│ (no views, no logic)          │ │ (no SwiftUI/UIKit)             │
└───────────────┬───────────────┘ └──────────────┬─────────────────┘
                │                                ▼
                │              ┌───────────────────────────────────┐
                │              │ TIER 2  PLATFORM                  │
                │              │ UsNetwork · UsAPI · UsRealtime ·  │
                │              │ UsStorage · UsSecurity · UsMedia ·│
                │              │ UsObservability · UsFeatureFlags  │
                │              └──────────────┬────────────────────┘
                ▼                             ▼
┌──────────────────────────────────────────────────────────────────┐
│ TIER 1  FOUNDATION                                               │
│ UsModel (entities, IDs) · UsDesignSystem (tokens, components)    │
└──────────────────────────────────────────────────────────────────┘
```

### 4.2 Dependency rules (checked in CI)

| Module kind | May depend on | Must not depend on |
|---|---|---|
| `UsModel` | Foundation | Anything else |
| `UsDesignSystem` | SwiftUI, `UsModel` | Tiers 2–5 |
| Tier 2 platform | `UsModel`, other Tier 2 modules (no cycles) | `UsDesignSystem`, Tiers 3–5 |
| `Feature<X>Interface`, `UsNavigation` | `UsModel` | Domain, platform, views |
| `Domain<X>` | Tier 2, `UsModel`, other Domain modules (no cycles) | SwiftUI, UIKit, Features, Interfaces |
| `Feature<X>` | Its own and other **Interfaces**, Domain, Tier 2, Tier 1 | **Any other `Feature<Y>`** |
| App | Everything | — |

- **Entities vs. DTOs:** `UsModel` holds domain entities. Wire DTOs are generated into `UsAPI` and mapped inside Domain repositories. Interfaces only pass entity IDs and small input structs.
- **Enforcement:** a CI script reads the package manifests (`swift package dump-package`) and fails on any edge that isn't on the allowlist above.

### 4.3 Granularity

The plan targets about **40 targets in R1**, not 120 or more. A feature is split into Interface and Implementation only once another feature needs to navigate to it or call it. Merge modules that always change together.

---

## 5. Directory & Package Blueprint

```
mobile/ios/
├── project.yml                         # XcodeGen spec (Us.xcodeproj is generated, gitignored)
├── App/
│   ├── Sources/
│   │   ├── UsApp.swift                 # @main; builds CompositionRoot lazily
│   │   ├── AppDelegate.swift           # PushKit registry + CallKit set up at launch (§7.2)
│   │   ├── CompositionRoot.swift       # DI container, feature factories
│   │   ├── RootTabView.swift           # Tab shell (tabs: decision D6)
│   │   ├── Navigation/
│   │   │   ├── TabRouter.swift         # Per-tab [AppRoute] stacks, sheets, covers
│   │   │   ├── RouteResolver.swift     # AppRoute → feature view
│   │   │   └── DeepLinkCoordinator.swift
│   │   └── Lifecycle/
│   │       ├── PushNotificationManager.swift
│   │       ├── CallKitCoordinator.swift
│   │       └── BackgroundTaskScheduler.swift
│   └── Resources/
│       ├── Info.plist
│       ├── PrivacyInfo.xcprivacy       # Privacy manifest (required)
│       ├── Localizable.xcstrings       # String Catalog
│       ├── Us.entitlements
│       └── Assets.xcassets
│
├── Extensions/
│   ├── UsNotificationService/          # Decrypt/enrich pushes, communication notifications
│   ├── UsLiveActivityWidget/           # R2: Food/Ride Live Activities
│   └── UsShareExtension/
│
└── Packages/
    ├── UsCore/                         # Tier 1
    │   └── Sources/ UsModel/ · UsDesignSystem/ (Tokens/, Components/, Haptics/)
    ├── UsPlatform/                     # Tier 2
    │   └── Sources/ UsNetwork/ · UsAPI/ (generated) · UsRealtime/ · UsStorage/ ·
    │                UsSecurity/ · UsMedia/ · UsObservability/ · UsFeatureFlags/ · UsTestSupport/
    ├── UsInterfaces/                   # Tier 3A
    │   └── Sources/ UsNavigation/ · FeatureProfileInterface/ · FeatureChatInterface/ ·
    │                FeatureCallingInterface/ · FeatureReelsInterface/ · FeatureCreateInterface/ · …
    ├── UsDomain/                       # Tier 3B
    │   └── Sources/ DomainAuth/ · DomainSocial/ · DomainMedia/ · DomainChat/ · DomainCalling/ ·
    │                DomainTrust/  ── R2+: DomainHyperlocal/ · DomainPayments/ · DomainCommerce/ · DomainMiniApps/
    └── UsFeatures/                     # Tier 4
        └── Sources/
            ├── R1: FeatureAuth/ · FeatureFeed/ · FeatureReels/ · FeatureStory/ · FeatureCreate/ ·
            │       FeatureChat/ · FeatureCalling/ · FeatureProfile/ · FeatureExplore/ · FeatureSafety/ ·
            │       FeatureSettings/   (includes in-app account deletion)
            ├── R2: FeatureServicesHub/ · FeatureFood/ · FeatureRides/ · FeaturePayments/ · FeatureBillPay/
            ├── R3: FeatureWatch/ · FeatureCreatorStudio/ · FeatureLive/ · FeatureCommunities/ · FeatureCommerce/
            └── R4: FeatureMiniAppHost/ · FeatureDating/ (or a separate app)
```

Each package has its own `Package.swift`, so a cycle between tiers can't build at all. The test target for each module lives next to it.

---

## 6. Navigation & Routing

- **One stack per tab:** `TabRouter` holds a `[AppRoute]` path for each tab plus the sheet and cover state. A typed array is used instead of `NavigationPath` because it can be inspected and is `Codable` for state restoration.
- **Routes:** each interface defines its own route enum (`ProfileRoute`, `ChatRoute`, …). `UsNavigation` wraps them:
  ```swift
  public enum AppRoute: Hashable, Codable, Sendable {
      case profile(ProfileRoute)
      case chat(ChatRoute)
      case reels(ReelsRoute)
      // …
  }
  ```
- **Navigating:** features receive a `Navigator` protocol (`push`, `present`, `dismiss`, `switchTab`) from the environment. They never construct another feature's views.
- **Destination resolution:** each tab's root has `.navigationDestination(for: AppRoute.self) { resolver.view(for: $0) }`. `RouteResolver` in the App target is the only place that knows the concrete feature views.
- **Deep links:** Universal Links, push payloads and the custom scheme are all parsed into `[AppRoute]` plus a target tab by `DeepLinkCoordinator`. Unknown or disabled routes (feature flag off) fall back to a safe screen. Every link format has a unit test.

---

## 7. Critical Platform Designs

### 7.1 Reels
- **Source:** HLS (`.m3u8`) with a multi-bitrate ladder and short segments for fast start.
- **Player pool:** three slots: previous (paused), current (playing), next (preloaded).
  - Each slot is an `AVQueuePlayer` with an `AVPlayerLooper`.
  - Set `preferredForwardBufferDuration` low for the next item and restore it once the item is current.
  - Load asset keys ahead of time with `load(.isPlayable, .duration)`.
  - Never create more players than the pool. Hardware decoders are limited.
- **Pager:** a Phase 0 spike decides between:
  - (a) a `UICollectionView` pager bridged into SwiftUI (precise prefetch and cell lifecycle; the default recommendation), or
  - (b) SwiftUI `ScrollView` with `.scrollTargetBehavior(.paging)` and `.scrollPosition(id:)`.
- **High frame rate:** add `CADisableMinimumFrameDurationOnPhone` to Info.plist to allow 120 Hz on ProMotion devices.
- **Adapting to conditions:** under Low Power Mode, thermal state `.serious` or higher, or on cellular, cap the bitrate and skip preloading.
- **Accessibility:** under Reduce Motion, don't autoplay. Show captions when available.

### 7.2 Calling (PushKit + CallKit + LiveKit)
1. Create the `PKPushRegistry` and `CXProvider` in `application(_:didFinishLaunchingWithOptions:)`, before anything else.
2. **Report every VoIP push to CallKit**, from `pushRegistry(_:didReceiveIncomingPushWith:for:completion:)`, calling `reportNewIncomingCall` before calling `completion`. If you don't, iOS terminates the app and eventually stops delivering VoIP pushes.
3. **Backend contract:**
   - Send a VoIP push only for call invites, with a short `apns-expiration`.
   - Send cancellations over the signaling WebSocket or a standard push, not VoIP.
   - If a stale invite arrives anyway, report it, then immediately end it with `reportCall(with:endedAt:reason:)`.
4. Connect to the LiveKit room on `CXAnswerCallAction` (incoming) or `CXStartCallAction` (outgoing).
5. **Audio session:** set the category to `.playAndRecord` / `.voiceChat` ahead of time, but **activate it only in `provider(_:didActivate:)`**. Turn off the LiveKit SDK's automatic audio-session management and drive it from CallKit (check current LiveKit docs for the API).
6. **Background modes:** `voip`, `audio`, `remote-notification`. Donate `INStartCallIntent` so calls appear in Recents and work with Siri.

### 7.3 Cold start
- `UsApp.init` builds only a lightweight container. Services are created lazily, and launch does no network work and no file I/O on the main actor.
- **Prewarming:** iOS may launch the process before the user taps the icon, and possibly before first unlock.
  - Check `isProtectedDataAvailable`.
  - Store session tokens with `kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly`.
- **Launch sequence:**
  1. Render the shell with skeletons.
  2. Open GRDB off the main actor.
  3. Hydrate cached feed and profile.
  4. Refresh the token (`POST /v1/auth/refresh`) and connect realtime in the background.
- If schema migrations are pending, run them behind a branded progress screen. Never block the first frame silently.
- **Measurement:** `XCTApplicationLaunchMetric` in CI on the reference simulator/device, and MetricKit launch histograms in production.

### 7.4 Storage & cross-process sharing

| Store | Location | Access | Data Protection |
|---|---|---|---|
| `app.sqlite` (feed, profiles, caches) | App container | App only | `completeUntilFirstUserAuthentication` |
| `chat.sqlite` (messages, FTS5 index) | App container | App only | `completeUntilFirstUserAuthentication` |
| `shared.sqlite` (minimal: conversation names/avatars for notifications, crypto session state in R2) | **App Group** | App + NSE | `completeUntilFirstUserAuthentication` |
| Tokens, keys | Keychain, **shared access group** | App + NSE | `AfterFirstUnlockThisDeviceOnly`; biometric items `.biometryCurrentSet` |

- **Keep the shared database small.** Use a GRDB `DatabasePool` and follow GRDB's "Sharing a Database" guide. Don't hold a SQLite lock when the app suspends: that triggers the `0xdead10cc` termination.
- Full `complete` protection would block notifications and VoIP handling while the device is locked, so it's used only for data the background paths never read.
- Media cache: size-capped LRU in `Caches/`, cleaned by a `BGProcessingTask`.

### 7.5 Notifications
- **Notification Service Extension:** fetch and decrypt the preview, attach a thumbnail, and donate `INSendMessageIntent` so the push shows as a **communication notification** with the sender's avatar.
- The extension has a small memory limit (about 24 MB) and roughly 30 s to run. Never load the full database there. Always deliver a fallback ("New message") if time runs out.
- Hiding notifications completely needs the `com.apple.developer.usernotifications.filtering` entitlement, which Apple must approve. Request it early if it's required.
- Notification categories with actions: Reply, Mark read, Accept/Decline (non-VoIP).

### 7.6 End-to-end encryption (R2)
- **Don't write a custom protocol.** Pick an audited library (decision D2):

| Option | Protocol | License | Notes |
|---|---|---|---|
| **MLS** (e.g. OpenMLS via UniFFI) | RFC 9420 | MIT | Built for groups and multi-device. Newer ecosystem. |
| **vodozemac** (Matrix) via UniFFI | Olm/Megolm (double ratchet) | Apache-2.0 | Proven in production, bridged from Rust. |
| libsignal | Signal Protocol | **AGPL-3.0** | Not usable in a closed-source app unless licensed separately. |

- Key storage: identity keys go in the shared Keychain group. The Secure Enclave is used only for P-256 device-attestation or signing keys.
- NSE decryption advances ratchet state, so the session store must be safe for the app and the NSE to write from different processes (§7.4). An alternative is a separate per-device "notification preview key" scheme.
- **Legal gate:** the traceability rule for significant social media intermediaries in the IT Rules 2021 (Rule 4(2)) may conflict with E2EE. Get a legal opinion before committing to the design (D4).
- Commission an external cryptography audit before GA.

### 7.7 Live Activities (R2)
- `RideStatusAttributes` and `OrderDeliveryAttributes`. Start them remotely with **push-to-start** tokens (iOS 17.2+). On iOS 17.0–17.1, start locally.
- The backend stores per-activity push tokens and sends `apns-push-type: liveactivity`.
  - Use priority 5 for routine ETA updates and priority 10 only for state changes, because priority 10 has a budget.
  - Set `NSSupportsLiveActivitiesFrequentUpdates` if needed.
- **Limits:** payload of 4 KB or less. An activity stays active up to 8 hours, then sits on the Lock Screen up to 4 more. Long rides or delayed deliveries need an "ended / see app" state.

---

## 8. Payments & Mini-Apps

### 8.1 Regulatory model (decision D1)
- A **stored balance** ("wallet", "Add Money") is a prepaid payment instrument and needs RBI PPI authorisation, or a partnership with a licensed issuer.
- If neither is in place for R2, the "wallet" is a **passbook**: transaction history plus linked UPI. There is no stored balance.
- The v1 term "non-custodial" is dropped as ambiguous.

### 8.2 UPI on iOS
- **Option A (R2 recommendation):** pay through a **licensed payment aggregator**.
  - App-switch to UPI apps using their URL schemes, declared in `LSApplicationQueriesSchemes`.
  - Confirm the payment status on the **server**, never from the app-switch callback.
- **Option B (later):** become a third-party app provider (TPAP) with a sponsor PSP bank and embed the NPCI-certified UPI SDK for in-app PIN entry. This brings heavy certification and compliance work.
- **QR scanning:** VisionKit, parsed into a `upi://pay` intent and validated server-side.

### 8.3 BBPS
- Integrate through a BBPS Operating Unit partner.
- Fetching and paying bills runs on the backend. The app only renders categories, billers and status.

### 8.4 Mini-apps (decision D3)
- **R1–R3:** first-party verticals only, built as native feature modules behind feature flags.
- **R4 third-party mini-apps** must follow App Review guidelines:
  - **2.5.2:** no downloaded native code. Mini-apps are HTML5/JS in `WKWebView`.
  - **4.7:** Apple permission is needed before exposing native APIs to mini-apps. Provide an index of mini-apps and their metadata. Digital goods must use in-app purchase. Data and permissions need explicit user consent.
  - **Host design:** a capability allowlist for the JS bridge, a per-mini-app permission prompt, a strict Content Security Policy, isolated `WKWebsiteDataStore` per mini-app, and operator review, reporting and age gating.

---

## 9. Security

| Control | Design |
|---|---|
| Session | Short-lived access JWTs and rotating refresh tokens in the Keychain (`ThisDeviceOnly`). On refresh-token reuse, the server revokes the session. |
| Device integrity | **App Attest** on OTP requests, login and payment initiation, with DeviceCheck as fallback. Jailbreak signals go to the backend as risk input; hard blocks apply only to payments. |
| Certificate pinning | Pin the SPKI of intermediate/CA certificates **with backup pins**. The pin set can be updated by signed remote config and has a kill switch. Rotation is rehearsed before GA. |
| Step-up auth | `LAContext` plus Keychain items with `.biometryCurrentSet`. A Secure Enclave P-256 key signs payment authorisations. |
| Screen privacy | Blur in the app switcher. Sensitive screens (payments, secret chats) react to `UIScreen.isCaptured`, and a screenshot notice is shown in secret chats. |
| Logging | `OSLog` with privacy annotations. No PII or tokens in logs or crash reports. |
| Review | A threat model for each vertical before it ships. An external penetration test before R1 and R2 GA. |

---

## 10. Compliance

### 10.1 App Store
- **Login (4.8):** Google Sign-In means offering an equivalent privacy-preserving login. **Sign in with Apple** is the straightforward way to meet this.
- **In-app purchase (3.1.1):**
  - Creator subscriptions, coins, live gifts and paid content must use in-app purchase.
  - Tips can skip in-app purchase only under **3.2.1(vi)**: the gift is optional, 100% reaches the creator, and nothing digital is unlocked.
  - Food, rides, bills and physical goods must use external payment (**3.1.3(e)**).
- **Account deletion (5.1.1(v))** in the app: `FeatureSettings`, in R1.
- **Privacy:** `PrivacyInfo.xcprivacy` declaring required-reason APIs, privacy manifests from third-party SDKs, accurate privacy nutrition labels, and ATT only if tracking occurs.
- **User-generated content (1.2):** filtering, reporting, blocking, and published contact information. This is in R1 (`FeatureSafety`).
- **Age rating:** a dating vertical pushes the **whole app** to the highest age rating. Recommendation: ship Pulse as a **separate app**, sharing packages and the account system.
- **Mini-apps:** see §8.4.
- Remove Apple Pay from the plan unless and until it launches in the target market.

### 10.2 India regulatory (legal owner)
- **DPDP Act 2023 and its Rules:** consent, notices, data-principal rights, and the approach for children's data. This affects onboarding and settings.
- **IT Rules 2021:** grievance officer and redressal flows (`FeatureSafety`), and the traceability question (§7.6).
- **RBI / NPCI:** PPI (§8.1), UPI partner rules, and BBPS partner rules.

---

## 11. Cross-Cutting Concerns

### 11.1 Observability
- **Crash reporting** with dSYM upload in CI.
- **MetricKit** payloads (launch, hangs, memory, disk writes, crash diagnostics) sent to the backend.
- `os_signpost` around feed load, reel start and message send, used in Instruments and `XCTOSSignpostMetric`.
- **Analytics:** a typed event schema in `UsObservability`, gated on consent, with dashboards per vertical.

### 11.2 Feature flags & remote config
- `UsFeatureFlags` keeps compiled-in defaults and a signed remote override.
- **Every vertical and risky path has a kill switch.**
- Releases are staged with App Store phased release plus flag ramp-ups. An experiment framework is added in R2.

### 11.3 Localization
- **String Catalogs** (`.xcstrings`). Launch languages are English and Hindi, with a regional-language roadmap.
- Use `en_IN`/`hi_IN` formatting for ₹ and lakh/crore grouping.
- Test layouts with long Indic strings. Support RTL if Urdu is planned.

### 11.4 Accessibility
- Dynamic Type up to the accessibility sizes, VoiceOver labels for every Design System component, Reduce Motion, captions, and contrast tokens that meet WCAG AA.
- Accessibility snapshot tests run in CI.

### 11.5 App size
- **Budget:** under 150 MB download for the reference device. This keeps the app well below the 200 MB cellular prompt.
- CI tracks the App Thinning size report and fails on regressions over 5%.
- Large optional content (AR filters, map packs) is delivered with the **Background Assets** framework.

### 11.6 Background work
- `BGAppRefreshTask` prefetches the feed. `BGProcessingTask` cleans the cache and maintains the database.
- Media uploads from Create use background `URLSession`.
- Silent pushes are treated as best-effort hints, never as guaranteed delivery.

### 11.7 API contract
- The backend publishes an **OpenAPI** spec. `UsAPI` is generated with swift-openapi-generator and versioned under `/v1`, with one standard error envelope.
- Realtime events are defined in a shared schema (JSON Schema or Protobuf). Contract tests run in CI against a staging mock.

---

## 12. Roadmap

```
R1 MVP (weeks are relative; pods run in parallel)
W1-2   ▕ P0 Decisions & spikes ▏
W3-6       ▕ P1 Foundation & CI ▏
W5-8           ▕ P2 Auth & shell ▏
W7-16              ▕ P3 Social & Media ──────────▏
W9-18                  ▕ P4 Messaging & Calling ─────▏
W17-22                                    ▕ P5 Safety, compliance, perf ▏
W22-26                                              ▕ P6 Beta & submission ▏
Then: R2 Hyperlocal & Payments → R3 Creator & Media → R4 Platform
```

### P0 — Decisions & spikes (W1–2)
- [ ] Close decisions D1–D8 (§16) or set firm dates for them.
- [ ] Spikes: the Reels pager (§7.1), GRDB App Group sharing between app and NSE, LiveKit + CallKit audio, SQLCipher (if needed), a cold-start baseline.
- [ ] Agree the reference device. Baseline the build time and the budgets in §1.

### P1 — Foundation & CI (W3–6) · Platform pod
- [ ] `project.yml`, the package skeletons, and the dependency-rule check (§4.2).
- [ ] `UsModel`; `UsDesignSystem` tokens and core components with snapshot and accessibility tests.
- [ ] `UsNetwork` (token refresh, pinning, retries), generated `UsAPI`, `UsStorage` (migrations, WAL, protection classes), `UsSecurity` (Keychain, App Attest).
- [ ] `UsObservability`, `UsFeatureFlags`, and a CI pipeline with selective testing (§13).

### P2 — Auth & shell (W5–8)
- [ ] Mobile OTP (protected by App Attest), **Sign in with Apple**, Google Sign-In, biometric step-up.
- [ ] Onboarding with DPDP consent.
- [ ] `TabRouter`, `RouteResolver`, `DeepLinkCoordinator` with Universal Links.
- [ ] `FeatureSettings` with in-app account deletion.

### P3 — Social & Media (W7–16) · Social pod
- [ ] Feed (post types, pagination, optimistic reactions, offline cache), Profile, Explore/Search.
- [ ] Reels (player pool, HLS, gestures, comments sheet) with budgets checked in CI.
- [ ] Stories viewer. Basic Create (capture, crop, background upload).

### P4 — Messaging & Calling (W9–18) · Messaging pod
- [ ] `UsRealtime` (reconnect with backoff and jitter, heartbeat, an outbox that survives restarts).
- [ ] Chat 1:1 and group (receipts, voice notes, media, FTS5 search).
- [ ] NSE with communication notifications.
- [ ] CallKit/PushKit/LiveKit audio and video, plus the backend push contract (§7.2).

### P5 — Safety, compliance, performance (W17–22)
- [ ] `FeatureSafety`: report, block, mute, grievance.
- [ ] Privacy manifest and nutrition labels.
- [ ] Localization (Hindi) and an accessibility audit.
- [ ] Instruments passes (Leaks, Allocations, Hitches) and a size budget check.
- [ ] Pen test and threat model sign-off.

### P6 — Beta & submission (W22–26)
- [ ] TestFlight external beta, a crash-free sessions target above 99.5%, phased release plan, and App Review submission.

### R2–R4
Each release starts with its own P0 (decisions and spikes) and is planned in detail at the end of the previous one. Every release follows the same quality gates.

---

## 13. Testing & CI

### Testing pyramid
1. **Unit tests** (Swift Testing). Coverage targets: 80% or more for `UsNetwork`, `UsStorage`, `UsSecurity`, `DomainAuth`, `DomainChat`, plus a test for every deep-link and route.
2. **Snapshot tests:** Design System components on iPhone SE (3rd gen) and a current Pro-size iPhone, in light/dark mode and at the largest Dynamic Type size. Add iPad only if decision D6 includes iPad.
3. **Integration tests:** `MockURLProtocol` and a mock WebSocket server cover offline outbox replay, token refresh races and pagination.
4. **UI tests** (XCUITest): smoke flows for login, posting, sending a message and answering a call (with the CallKit simulator).
5. **Performance tests:** `XCTApplicationLaunchMetric`, `XCTOSSignpostMetric` for reel start and scroll hitches, and `XCTMemoryMetric`, all with baselines.

### CI pipeline
- **On every PR:**
  ```bash
  xcodegen generate
  scripts/check-module-deps.sh
  scripts/affected-packages.sh origin/main | xargs -I{} xcodebuild test -scheme {} -destination 'platform=iOS Simulator,name=iPhone 17,OS=latest' -enableCodeCoverage YES
  swiftlint lint --strict
  ```
  `swift test` isn't used: packages that import UIKit, AVFoundation or CallKit only build for iOS.
- **Nightly:** the full test suite, performance tests against baselines, the size report, and a TestFlight internal build via fastlane (or Xcode Cloud).
- **Pinning:** the Xcode version and simulator runtime are pinned in CI and updated deliberately.

---

## 14. Ownership
- `CODEOWNERS` assigns an owner pod to each package. Changes to Tier 1–2 APIs need Platform pod review.
- An architecture decision record (ADR) is written for every decision in §16 and every change to the dependency rules.

---

## 15. Risks

| Risk | Impact | Mitigation |
|---|---|---|
| Scope creep from 18 verticals | Missed R1 | Freeze R1 scope; gate new verticals behind releases and flags |
| App Review rejection (in-app purchase, 4.7, dating, UGC) | Launch delay | Compliance checklist (§10) reviewed at every release P0 |
| Payments licensing not in place | R2 slip | Passbook fallback (§8.1); payment aggregator route |
| E2EE complexity or legal conflict | R2 slip, legal exposure | Defer to R2; audited library; legal opinion (D4) |
| VoIP push misuse | Calls stop working | Backend contract (§7.2) plus monitoring of report and failure rates |
| Build times as modules grow | Lower team velocity | Granularity rule (§4.3), selective CI, evaluate Tuist caching |
| Performance regressions | Poor reviews | Budgets in CI, MetricKit dashboards, reference device |

---

## 16. Open Decisions (must close in P0 unless noted)

| ID | Decision | Options | Owner |
|---|---|---|---|
| D1 | Payments model | PPI licence/partner vs. passbook-only | Product + Legal |
| D2 | E2EE library | MLS (OpenMLS) / vodozemac / licensed libsignal | Security + iOS lead (by start of R2) |
| D3 | Mini-app model | First-party only vs. third-party HTML5 (R4) | Product |
| D4 | Traceability vs. E2EE | Legal opinion | Legal |
| D5 | Pulse packaging | Separate app vs. in-app with 18+ rating | Product |
| D6 | Tab structure & iPad | Which 5 tabs (Chat? Reels?); iPhone-only vs. iPad | Product + Design |
| D7 | Vendors | Crash reporting, analytics, payment aggregator, BBPS partner | Eng + Business |
| D8 | Team & timeline | Confirm the ~10-engineer, 3-pod assumption | Eng management |

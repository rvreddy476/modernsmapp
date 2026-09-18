pluginManagement {
    // build-logic is a composite build: its convention plugins are available
    // to every module below by plugin id (us.android.*), with no buildscript
    // classpath juggling and no version strings outside the catalog.
    includeBuild("build-logic")
    repositories {
        google {
            content {
                includeGroupByRegex("com\\.android.*")
                includeGroupByRegex("com\\.google.*")
                includeGroupByRegex("androidx.*")
            }
        }
        mavenCentral()
        gradlePluginPortal()
    }
}

dependencyResolutionManagement {
    // Modules must not declare their own repositories. One place, one order.
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        google {
            content {
                includeGroupByRegex("com\\.android.*")
                includeGroupByRegex("com\\.google.*")
                includeGroupByRegex("androidx.*")
            }
        }
        mavenCentral()
        // JitPack, scoped to the ONE artifact that needs it: LiveKit's
        // audioswitch fork rides a commit hash only JitPack serves.
        maven("https://jitpack.io") {
            content { includeGroup("com.github.davidliu") }
        }
        // Banuba Video Editor SDK, scoped to its own group. Anonymous, no
        // credentials: the licence lives in the token, not in the repository.
        maven("https://nexus.banuba.net/repository/maven-releases") {
            name = "nexus"
            content { includeGroup("com.banuba.sdk") }
        }
    }
}

rootProject.name = "us-android"

enableFeaturePreview("TYPESAFE_PROJECT_ACCESSORS")

// ── Phase 0 modules ────────────────────────────────────────────────────
include(":app")
include(":core:common")
include(":core:model")
include(":core:designsystem")
include(":core:testing")

// ── Phase 1 modules ────────────────────────────────────────────────────
include(":core:datastore")
include(":core:database")
include(":core:creator-model")
include(":core:creator-engine")
include(":core:telemetry")
include(":core:network")
include(":core:auth")

// ── Phase 2 modules ────────────────────────────────────────────────────
include(":feature:auth")

// ── Phase 6 modules (profile vertical slice) ───────────────────────────
// :core:ui holds product-neutral composites assembled from design-system
// primitives. It arrives with the first feature that needs them rather than
// as an empty framework built ahead of a consumer.
include(":core:ui")
include(":feature:profile")
include(":feature:post")
include(":feature:feed")
include(":core:media")
include(":core:notifications")
include(":core:profile")
include(":core:engagement")
include(":core:chat")
include(":feature:chat")

// Voice & video calling (calling P0). :core:call owns the data seam, the
// signaling protocol over the ONE session socket, and the WebRTC engine;
// :feature:call renders the incoming/outgoing/in-call surfaces.
include(":core:call")
include(":feature:call")

// Live streaming (live-service-v2 + LiveKit): go-live, live-now, watching.
include(":feature:live")

// The notification inbox — Slice D. A feature module rather than screens bolted
// onto :feature:feed: the inbox is its own destination with its own paging and
// read-state, and putting it in the feed would make the feed depend on
// :core:notifications for a surface it does not render.
include(":feature:notifications")

// The module picker: first-login onboarding and its settings-hub twin. Its
// own feature so neither :feature:profile nor :app carries a screen that has
// to appear BEFORE the tabs exist.
include(":feature:settings")

// The feed data seam — feed-service endpoints, paging, hydration, the follow
// graph, and the shared post "more" / comments sheets. Split out of
// :feature:feed when Tube arrived: features must not depend on each other.
include(":core:feed")

// Tube — long video: the home list, the watch screen. Reads through
// :core:feed; posts through :feature:post's video pipeline.
include(":feature:tube")

// Search (founder, 2026-09-05): one page, scoped by where it was opened from
// — Home, Reels, the video app, Explore. Its own feature so none of those
// carries a screen the others also need.
include(":feature:search")
// ── Commerce modules (P0 launch loop) ──────────────────────────────────
//
// :core:commerce owns the DTOs, the repository, the domain models and the
// Paise money type; :feature:commerce owns the screens. The split is the one
// every other vertical here uses, and it is what lets the money contract be
// enforced in a single place: Paise is a value class over Long, so a Double
// amount is a compile error rather than a review comment.
//
// There is deliberately no :feature:* → :feature:* edge from commerce. Its
// entry points are routes on UsNavHost, wired in :app. The rule is enforced
// by the `checkFeatureGraph` task, not by convention.
include(":core:commerce")
include(":feature:commerce")

// Product analytics — the client half of analytics-service (2026-09-07).
//
// SEPARATE FROM :core:telemetry ON PURPOSE.
//
// :core:telemetry is OpenTelemetry: operational metrics and spans, exported to
// an OTLP collector by the OTel SDK's own sender, and its KDoc explicitly
// FORBIDS a post id, content id or user id as a dimension — high-cardinality
// labels are what turn a metrics bill into an incident.
//
// This module is the opposite by design: every event is keyed by content id
// and creator id, because it feeds view counts, the content quality score and
// ultimately creator payouts. It also needs three things :core:telemetry has
// none of — the app's authenticated OkHttp stack, a disk-backed queue that
// survives process death, and WorkManager. Folding it in would have added all
// four dependencies to a module whose whole value is being small and having
// no product knowledge.
//
// It is not :core:media either: half the events here (like, comment, share,
// save, follow-from-content, not-interested, report) have nothing to do with
// a player.
include(":core:analytics")

// Face AR / virtual try-on (2026-09-12).
//
// A CORE module rather than screens inside :feature:commerce, for the reason
// that already put the photo editor PORT in :core:ui: the licence gate, the
// try-on domain and the camera surface are wanted by more than one product
// (commerce first, reels and profile plausibly next) and a :feature:* →
// :feature:* edge is forbidden by `checkFeatureGraph`.
//
// It owns the Banuba FACE AR SDK (1.17.6) — a different product line from the
// Video Editor SDK (1.54.1) that :feature:post owns, on the SAME licence
// token. The two initialise the native SDK by different entry points in one
// process; see FaceArGate's header for the unverified-combination warning.
include(":core:facear")

// Feast / Kitchen / Rider — A1 (2026-09-13).
//
// :core:realtime is the SSE client for notification-service's
// /v1/realtime/sse: the framing parser, Last-Event-ID resume, jittered
// backoff, topic-token refresh through a RealtimeTokenSource port, and the
// lifecycle binding. It knows no domain, so rider and any later vertical
// reuse it rather than growing a second stream client.
//
// :core:food owns the food-service DTOs (pinned by the golden contract
// fixtures), the repository, the onboarding checklist and food's realtime
// token source. No app depends on either yet; A3–A5 wire them in.
include(":core:realtime")
include(":core:food")

// Feast Kitchen — A3 (2026-09-13).
//
// :feature:kitchen is the restaurant partner's screens: the role gate,
// onboarding (location, compliance, FSSAI, payout, hours), the menu editor,
// the live order queue and earnings. :app-kitchen is its own installable
// (applicationId com.us.feast.kitchen, proposed — founder confirms before
// Play). Neither is reachable from :app; the application-boundary rules in
// the root build file keep Banuba, the creator engine, posting and commerce
// out of the Kitchen APK.
include(":feature:kitchen")
include(":app-kitchen")

// Feast Rider — A4 (2026-09-13).
//
// :feature:rider is the delivery partner's screens: the role gate and "become a
// rider", verification (vehicle, DigiLocker, DL/RC, selfie, payout), going
// online with the location foreground service, job offers and the active job.
// :app-rider is its own installable (applicationId com.us.feast.rider,
// proposed). Neither is reachable from :app; the same application-boundary
// rules as Kitchen keep Banuba, the creator engine, posting and commerce out.
include(":feature:rider")
include(":app-rider")

// Payments — the reusable payment sheet (2026-09-14).
//
// :core:payments owns the PSP SDK (Razorpay), the one-flight launcher, the
// SDK-outcome mapping, the Activity binding and the generic coordinator that
// opens a sheet and then polls a product's PaymentStatusSource until the
// SERVER says paid, failed or refunded. It was :app's payment package; Momentum
// commerce checkout is its first product and Feast checkout its next. It
// depends on no :feature:*, no :app* and no :core:commerce, and the partner
// apps may not depend on it — both enforced by moduleGraphCheck.
include(":core:payments")
include(":feature:feast")

// Dating (Pulse) — Wave 3 (2026-09-16).
//
// :feature:dating is the dating product inside Momentum: onboarding with consent,
// photos and the blink-twice selfie video, the Pulse deck, sparks, matches,
// safety, Premium passes through :core:payments, privacy and data rights. Only
// :app depends on it; moduleGraphCheck keeps it out of the partner apps and off
// every other :feature:*.
include(":feature:dating")

// Mopedu — ride-hailing (2026-09-18), ported from the Gemini branch.
//
// :core:mobility-model is pure Kotlin/JVM: the ride, quote, payment and captain
// domain types, all money in integer paise, no serialization annotations
// (DTOs live in each feature's data layer). :feature:mopedu-rider is the
// customer's ride flow inside Momentum — quote with surge and coupons, book,
// track, OTP, pay through :core:payments (application "mopedu"), receipt,
// outstanding cancellation fees. :feature:mopedu-captain is the driver's
// screens: onboarding, on/offline with a location foreground service, offers,
// the trip, collecting payment, earnings. :app-captain is its own installable
// (applicationId com.us.mopedu.captain, proposed — founder confirms before
// Play). moduleGraphCheck keeps the rider feature to :app, the captain feature
// to :app-captain, and :core:payments out of the captain APK.
include(":core:mobility-model")
include(":feature:mopedu-rider")
include(":feature:mopedu-captain")
include(":app-captain")

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

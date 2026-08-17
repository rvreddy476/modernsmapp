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

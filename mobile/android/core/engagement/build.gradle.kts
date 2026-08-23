plugins {
    id("us.android.library")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.core.engagement"
}

// The neutral engagement seam.
//
// Reactions, bookmarks, reposts, comments and external shares are performed
// from the feed, from post detail, and later from profile and search. Putting
// them here is what stops :feature:feed depending on :feature:post just to
// reuse a mutation — a feature-to-feature edge the module graph check forbids,
// and which would drag post-detail UI into the feed's compile graph.
//
// Data only. No Compose, no screen state. A component that can fetch is a
// component that cannot be previewed or reused by a feature whose data comes
// from somewhere else.
dependencies {
    implementation(projects.core.common)
    // Retrofit, the shared envelope, apiCall/pagedApiCall. This module
    // declares endpoint interfaces; it never builds a client.
    implementation(projects.core.network)

    implementation(libs.kotlinx.serialization.json)

    testImplementation(projects.core.testing)
    testImplementation(libs.junit)
    testImplementation(libs.truth)
    testImplementation(libs.kotlinx.coroutines.test)
    testImplementation(libs.okhttp.mockwebserver)
    testImplementation(libs.retrofit.kotlinx.serialization)
}

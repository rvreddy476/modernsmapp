plugins {
    id("us.android.library")
    id("us.android.compose")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.core.memberships"
}

// Paid creator memberships (2026-10-23) — the shared half.
//
// monetization-service's membership routes (levels, eligibility, join, renew,
// change level, cancel, my memberships, the payment status poll) and
// post-service's members-only gate (PUT /v1/posts/:id/membership), their wire
// types, the level rules the server enforces, the payment flow on
// :core:payments as application "memberships", and the pieces every surface
// that shows a members-only post draws: the Join sheet, the lock card and the
// "Members only" level picker.
//
// A CORE module because those pieces are wanted by Reels (:feature:feed), the
// watch page and channel (:feature:tube), the profile (:feature:profile), the
// composers (:feature:post) and the comments sheet (:core:feed), and a
// :feature:* -> :feature:* edge is forbidden. The creator's Memberships page
// and the member's own list are screens, in :feature:memberships.
//
// moduleGraphCheck rule (n) keeps it free of every :feature:* and every app,
// and out of the partner apps.
dependencies {
    api(projects.core.model)
    implementation(projects.core.common)
    implementation(projects.core.designsystem)
    implementation(projects.core.ui)
    // ApiEnvelope, apiCall, ErrorMapper and the app-wide Retrofit.
    implementation(projects.core.network)
    // The payment sheet, the confirm-by-polling coordinator, the in-flight
    // record and the handoff bus, scoped to "memberships".
    api(projects.core.payments)

    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.hilt.navigation.compose)
    implementation(libs.kotlinx.serialization.json)

    testImplementation(projects.core.testing)
    testImplementation(libs.okhttp.mockwebserver)
    testImplementation(libs.retrofit.kotlinx.serialization)
}

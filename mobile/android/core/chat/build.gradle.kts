plugins {
    id("us.android.library")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.core.chat"
}

// The chat data seam.
//
// SEPARATE FROM :core:engagement ON PURPOSE.
//
// Comments and chat look alike on screen and are not alike underneath.
// Comments are durable, threaded and cursor-paginated against post-service;
// chat is realtime, time-ordered, socket-delivered and has its own retention.
// Forcing messages through CommentsController to reuse a screen would put a
// realtime transport behind a pagination API and give both products one set of
// failure modes.
//
// What IS shared is the design system: avatar, text field, scaffold, state
// views. Those live in :core:designsystem and :core:ui and are consumed by the
// chat feature, exactly as the architecture decision requires.
//
// Data only. No Compose, no screen state.
dependencies {
    implementation(projects.core.common)
    // Retrofit, the shared envelope and apiCall. This module declares endpoint
    // interfaces; it never builds a client.
    implementation(projects.core.network)
    // Production chat pass: the durable chat boundary — Room-backed inbox
    // cache, message cache and the send outbox live in :core:database; this
    // module owns the store/worker logic over them.
    implementation(projects.core.database)
    implementation(libs.work.runtime)
    implementation(libs.hilt.work)
    ksp(libs.hilt.work.compiler)

    implementation(libs.kotlinx.serialization.json)
    implementation(libs.okhttp)

    // Chat lock (P0-7): Keystore-wrapped verifier storage + a reviewed
    // memory-hard KDF (argon2id) — the directive's binding requirements for
    // the PIN verifier; PBKDF2 survives only as the upgrade path.
    implementation(libs.androidx.security.crypto)
    implementation(libs.bouncycastle.provider)

    testImplementation(projects.core.testing)
    testImplementation(libs.junit)
    testImplementation(libs.truth)
    testImplementation(libs.kotlinx.coroutines.test)
    testImplementation(libs.okhttp.mockwebserver)
    testImplementation(libs.retrofit.kotlinx.serialization)
}

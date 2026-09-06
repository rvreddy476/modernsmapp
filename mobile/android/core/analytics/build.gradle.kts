plugins {
    id("us.android.library")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.core.analytics"
}

// The product-analytics client — the app half of analytics-service.
//
// Data only. No Compose, no screen state: the player surfaces call into
// VideoWatchTracker and the action view models call into AnalyticsClient, and
// neither gets a UI dependency back.
//
// It declares an endpoint interface and NEVER builds its own OkHttp client —
// same rule :core:chat and :core:commerce follow. A bespoke client here would
// fork token refresh, and telemetry is the last thing that should be able to
// sign a user out.
dependencies {
    implementation(projects.core.common)
    implementation(projects.core.model)
    // Retrofit, the shared envelope, apiCall and the AppResult error mapping.
    implementation(projects.core.network)
    // The durable outbox table lives with every other table, in one database.
    implementation(projects.core.database)
    // SessionStateProvider only: analytics is dropped while signed out, and
    // the server rebuilds attribution from the gateway actor regardless.
    implementation(projects.core.auth)

    implementation(libs.work.runtime)
    implementation(libs.hilt.work)
    ksp(libs.hilt.work.compiler)

    implementation(libs.kotlinx.serialization.json)
    implementation(libs.kotlinx.coroutines.android)

    testImplementation(projects.core.testing)
    testImplementation(libs.okhttp.mockwebserver)
    testImplementation(libs.retrofit.kotlinx.serialization)
}

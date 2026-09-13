plugins {
    id("us.android.library")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.core.food"
}

dependencies {
    api(projects.core.common)
    // Retrofit, the shared ApiEnvelope and the authenticated client. Like
    // :core:commerce, this module declares endpoint interfaces and NEVER
    // builds its own client — that would bypass token refresh and tracing.
    api(projects.core.network)
    // The RealtimeTokenSource port, implemented here by FoodRealtimeTokenSource.
    api(projects.core.realtime)

    api(libs.retrofit)
    api(libs.kotlinx.serialization.json)
    implementation(libs.retrofit.kotlinx.serialization)
    implementation(libs.kotlinx.coroutines.android)

    testImplementation(projects.core.testing)
    testImplementation(libs.okhttp.mockwebserver)
}

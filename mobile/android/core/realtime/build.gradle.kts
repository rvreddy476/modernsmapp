plugins {
    id("us.android.library")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.core.realtime"
}

dependencies {
    api(projects.core.common)
    // The shared authenticated OkHttp client, ApiConfig and Json. The SSE
    // client is DERIVED from that client (same interceptors for headers, auth
    // and tracing) and never built from scratch — see RealtimeModule.
    api(projects.core.network)

    api(libs.okhttp)
    api(libs.kotlinx.serialization.json)
    api(libs.kotlinx.coroutines.core)
    // Only to strip the shared logging interceptor from the streaming client:
    // BASIC logging prints the URL, and the topic token is a query parameter.
    implementation(libs.okhttp.logging)
    implementation(libs.kotlinx.coroutines.android)
    // repeatOnLifecycle for collectWhileStarted.
    api(libs.androidx.lifecycle.runtime.ktx)

    testImplementation(projects.core.testing)
    testImplementation(libs.okhttp.mockwebserver)
}

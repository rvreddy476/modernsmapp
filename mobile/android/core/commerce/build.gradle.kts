plugins {
    id("us.android.library")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.core.commerce"
}

dependencies {
    api(projects.core.model)
    api(projects.core.common)
    // Retrofit, the shared ApiEnvelope, apiCall, the token authenticator and
    // the tracing/retry interceptors. LB-A3: this module declares endpoint
    // interfaces and NEVER builds its own client — a bespoke OkHttp here
    // would bypass token refresh and trace propagation.
    api(projects.core.network)

    api(libs.retrofit)
    api(libs.kotlinx.serialization.json)
    implementation(libs.retrofit.kotlinx.serialization)
    implementation(libs.kotlinx.coroutines.android)

    testImplementation(projects.core.testing)
    testImplementation(libs.okhttp.mockwebserver)
}

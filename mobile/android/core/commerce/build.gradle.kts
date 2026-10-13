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
    // The try-on VOCABULARY only — TryOnDescriptor, TryOnKind, TryOnVariant —
    // which a Product carries and which :feature:commerce reads, so `api`.
    //
    // Share vocabulary without putting the camera-effects SDK in the Partner
    // APK. The customer-only :core:facear module depends on these same types.
    api(projects.core.facearModel)

    api(libs.retrofit)
    api(libs.kotlinx.serialization.json)
    implementation(libs.retrofit.kotlinx.serialization)
    implementation(libs.kotlinx.coroutines.android)

    testImplementation(projects.core.testing)
    testImplementation(libs.okhttp.mockwebserver)
}

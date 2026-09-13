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
    // This does NOT put the Banuba SDK on this module's compile classpath:
    // :core:facear declares the vendor artifacts as `implementation`, so what
    // crosses this edge is four pure Kotlin types. The alternative was a
    // second copy of the kind enum and the variant type in this module, and a
    // wire parser whose vocabulary can drift from the renderer's.
    api(projects.core.facear)

    api(libs.retrofit)
    api(libs.kotlinx.serialization.json)
    implementation(libs.retrofit.kotlinx.serialization)
    implementation(libs.kotlinx.coroutines.android)

    testImplementation(projects.core.testing)
    testImplementation(libs.okhttp.mockwebserver)
}

plugins {
    id("us.android.library")
    id("us.android.compose")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.feature.profile"
}

dependencies {
    implementation(projects.core.model)
    implementation(projects.core.common)
    implementation(projects.core.designsystem)
    implementation(projects.core.ui)
    // For Retrofit, the shared envelope and apiCall. This feature declares
    // endpoint interfaces; it never builds a client.
    implementation(projects.core.network)

    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.hilt.navigation.compose)
    implementation(libs.kotlinx.serialization.json)

    testImplementation(projects.core.testing)
    testImplementation(libs.okhttp.mockwebserver)
    // The converter is `implementation` in :core:network by design, so it does
    // not leak onto this classpath. Contract tests build their own Retrofit
    // against MockWebServer and need it explicitly.
    testImplementation(libs.retrofit.kotlinx.serialization)
}

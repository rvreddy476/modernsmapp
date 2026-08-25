plugins {
    id("us.android.library")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.core.profile"
}

dependencies {
    implementation(projects.core.model)
    implementation(projects.core.common)
    // For Retrofit, the shared envelope and apiCall. This module declares
    // endpoint interfaces; it never builds a client.
    implementation(projects.core.network)

    implementation(libs.kotlinx.serialization.json)

    testImplementation(projects.core.testing)
    testImplementation(libs.okhttp.mockwebserver)
    testImplementation(libs.retrofit.kotlinx.serialization)
}

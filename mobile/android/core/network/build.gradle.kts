plugins {
    id("us.android.library")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.core.network"
}

dependencies {
    api(projects.core.model)
    api(projects.core.common)
    implementation(projects.core.datastore)
    // api, not implementation: :core:auth records operations through the same
    // Telemetry instance, so the type must be visible to consumers.
    api(projects.core.telemetry)

    api(libs.okhttp)
    api(libs.retrofit)
    api(libs.kotlinx.serialization.json)
    implementation(libs.okhttp.logging)
    implementation(libs.retrofit.kotlinx.serialization)
    implementation(libs.kotlinx.coroutines.android)

    testImplementation(projects.core.testing)
    testImplementation(libs.okhttp.mockwebserver)
}

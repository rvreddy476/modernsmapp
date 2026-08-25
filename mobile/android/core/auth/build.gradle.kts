plugins {
    id("us.android.library")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.core.auth"
}

dependencies {
    api(projects.core.model)
    api(projects.core.common)
    api(projects.core.network)
    implementation(projects.core.datastore)

    implementation(libs.kotlinx.serialization.json)
    implementation(libs.kotlinx.coroutines.android)

    testImplementation(projects.core.testing)
    testImplementation(libs.okhttp.mockwebserver)
    // The converter is `implementation` in :core:network by design, so it
    // does not leak onto this module's test classpath. Tests here build their
    // own Retrofit against MockWebServer and need it explicitly.
    testImplementation(libs.retrofit.kotlinx.serialization)
}

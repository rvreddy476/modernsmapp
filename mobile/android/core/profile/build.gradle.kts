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
    // The module-preferences cache: the last server answer survives a relaunch
    // so the shell never waits on the network to draw the user's own tabs.
    implementation(projects.core.datastore)

    implementation(libs.kotlinx.coroutines.android)
    implementation(libs.kotlinx.serialization.json)

    testImplementation(projects.core.testing)
    testImplementation(libs.okhttp.mockwebserver)
    testImplementation(libs.retrofit.kotlinx.serialization)
}

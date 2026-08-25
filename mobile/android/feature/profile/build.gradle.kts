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
    implementation(projects.core.profile)
    implementation(projects.core.auth)
    implementation(projects.core.media)
    // Start-direct only: the Message button on someone else's profile opens a
    // conversation through :core:chat. This is a feature depending on a core
    // module, which the graph rule allows; :feature:chat is NOT a dependency
    // and the chat screens stay unreachable from here — :app decides that the
    // returned conversation opens the thread destination.
    implementation(projects.core.chat)
    // For Retrofit, the shared envelope and apiCall. This feature declares
    // endpoint interfaces; it never builds a client.
    implementation(projects.core.network)

    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.hilt.navigation.compose)
    implementation(libs.kotlinx.serialization.json)
    implementation(libs.coil.compose)

    testImplementation(projects.core.testing)
    testImplementation(libs.okhttp.mockwebserver)
    // The converter is `implementation` in :core:network by design, so it does
    // not leak onto this classpath. Contract tests build their own Retrofit
    // against MockWebServer and need it explicitly.
    testImplementation(libs.retrofit.kotlinx.serialization)
}

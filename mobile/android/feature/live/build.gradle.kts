plugins {
    id("us.android.library")
    id("us.android.compose")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.feature.live"
}

// Live streaming against live-service-v2: the go-live surface (publisher),
// the live-now list, and watching (viewer + chat).
dependencies {
    implementation(projects.core.common)
    implementation(projects.core.designsystem)
    implementation(projects.core.network)
    implementation(projects.core.ui)
    // WebLinks (the room's Share address) and FollowStatus.
    implementation(projects.core.model)
    // Follow in the room writes through the app's shared FollowGraph, which
    // also knows the signed-in user's id (T-30, 2026-10-15).
    implementation(projects.core.feed)
    // The room's realtime frames: ws-gateway's socket on the shared client.
    implementation(libs.okhttp)

    // The LiveKit client SDK. It bundles the SAME webrtc-sdk org.webrtc the
    // calling stack compiles against (see :core:call) — one native copy.
    implementation(libs.livekit.android)

    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.hilt.navigation.compose)
    // Runtime camera/mic permission prompts before going live.
    implementation(libs.androidx.activity.compose)
    implementation(libs.kotlinx.serialization.json)

    testImplementation(projects.core.testing)
    testImplementation(libs.junit)
    testImplementation(libs.truth)
    testImplementation(libs.kotlinx.coroutines.test)
    // Request shapes pinned against a local server (LiveApiRequestTest).
    testImplementation(libs.okhttp.mockwebserver)
    testImplementation(libs.retrofit.kotlinx.serialization)
}

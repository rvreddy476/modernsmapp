plugins {
    id("us.android.library")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.core.call"
}

// The calling data seam + engine (calling P0).
//
// SEPARATE FROM :core:chat ON PURPOSE, but layered on it: call signaling
// rides the ONE session socket :core:chat owns (a second socket forks token
// refresh), while everything call-specific — the REST client, the signaling
// protocol, the WebRTC engine, the call state machine — lives here so chat
// never learns what an SDP is.
//
// Data + engine only. No Compose; the screens live in :feature:call.
dependencies {
    implementation(projects.core.common)
    implementation(projects.core.model)
    // Retrofit + the shared envelope + apiCall/listApiCall.
    implementation(projects.core.network)
    // The session socket (ChatSessionManager.sendCallFrame / events).
    implementation(projects.core.chat)
    // The CALLS notification channel + push-key contract.
    implementation(projects.core.notifications)

    implementation(libs.kotlinx.serialization.json)
    implementation(libs.androidx.core.ktx)

    // Prebuilt libwebrtc: PeerConnection, Camera2 capture, EGL rendering.
    api(libs.stream.webrtc.android)

    testImplementation(projects.core.testing)
    // The session-manager fakes construct :core:chat's real collaborators.
    testImplementation(projects.core.database)
    testImplementation(libs.junit)
    testImplementation(libs.truth)
    testImplementation(libs.kotlinx.coroutines.test)
    testImplementation(libs.okhttp.mockwebserver)
    testImplementation(libs.retrofit.kotlinx.serialization)
}

plugins {
    id("us.android.library")
    id("us.android.compose")
    id("us.android.hilt")
    // Navigation Compose type-safe routes are @Serializable objects resolved
    // at RUNTIME — same requirement (and same failure mode when absent) as
    // every other feature module with a route.
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.feature.call"
}

// Screens only (calling P0): the outgoing/incoming/in-call surfaces and the
// call history list. Every behaviour they render — the state machine, the
// signaling protocol, the WebRTC engine — lives in :core:call.
dependencies {
    implementation(projects.core.call)
    implementation(projects.core.common)
    implementation(projects.core.designsystem)
    implementation(projects.core.model)

    implementation(libs.androidx.lifecycle.viewmodel.compose)
    // ON_RESUME re-reads the real permission grants (CALL-LB-6).
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.hilt.navigation.compose)
    // Runtime mic/camera permission prompts.
    implementation(libs.androidx.activity.compose)

    testImplementation(projects.core.testing)
    testImplementation(libs.junit)
    testImplementation(libs.truth)
    testImplementation(libs.kotlinx.coroutines.test)
    // TEST-ONLY (CALL-LB-6): the permission-journey test composes the REAL
    // CallScreen over a real CallSessionManager, which needs the chat
    // session/network fakes on the unit-test classpath. Robolectric drives
    // it on the same `testDebugUnitTest` the gate runs — same rationale as
    // :feature:chat's navigation test.
    testImplementation(projects.core.chat)
    testImplementation(projects.core.database)
    testImplementation(projects.core.network)
    testImplementation(platform(libs.compose.bom))
    testImplementation(libs.compose.ui.test.junit4)
    debugImplementation(libs.compose.ui.test.manifest)
}

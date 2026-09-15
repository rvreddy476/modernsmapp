plugins {
    id("us.android.library")
    id("us.android.compose")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.feature.dating"
}

// Dating (Pulse) inside Momentum — Wave 3, 2026-09-16: onboarding with consent,
// photos and a blink-twice selfie video, the Pulse deck, sparks and matches,
// safety (report, block, panic, trusted contacts, live location), Premium
// passes paid through :core:payments, privacy and data rights.
//
// Depended on ONLY by :app. The root moduleGraphCheck forbids the partner apps
// from reaching this module, and forbids this module from reaching ANY other
// :feature:* — directly or through a core module. Chat opens through a
// callback :app supplies; the conversation itself is created server-side.
dependencies {
    implementation(projects.core.common)
    implementation(projects.core.designsystem)
    // ApiEnvelope, ApiConfig (the photo routes are gateway-relative), Retrofit.
    implementation(projects.core.network)
    // MediaUploader: reserve → presigned PUT → confirm for photos and the selfie video.
    implementation(projects.core.media)
    // The payment sheet, the confirm-by-polling coordinator, the in-flight
    // record and the handoff bus — shared with MStore and Feast, scoped to "dating".
    implementation(projects.core.payments)

    // Fused location for the profile's snapped point, panic and live share.
    implementation(libs.play.services.location)

    // The selfie VIDEO: CameraX core only, 1.4.1 (the version Banuba Face AR
    // pins in :app). camera-video is NOT in the offline cache, so the clip is
    // recorded by the platform MediaRecorder from a Preview surface
    // (selfie/SelfieVideoCamera.kt).
    implementation(libs.androidx.camera.core)
    implementation(libs.androidx.camera.camera2)
    implementation(libs.androidx.camera.lifecycle)

    // Photos through the app's singleton loader, which shares the authenticated
    // OkHttp client (bearer on the API origin only, redirects followed).
    implementation(libs.coil.compose)

    implementation(libs.androidx.core.ktx)
    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.hilt.navigation.compose)
    implementation(libs.kotlinx.serialization.json)
    implementation(libs.kotlinx.coroutines.android)

    testImplementation(projects.core.testing)
    testImplementation(libs.kotlinx.coroutines.test)
}

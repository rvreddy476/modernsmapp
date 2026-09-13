plugins {
    id("us.android.library")
    id("us.android.compose")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.feature.rider"
}

// Feast Rider's screens (A4, 2026-09-13): the role gate and "become a rider",
// verification (vehicle, DigiLocker, DL/RC, selfie, payout), going online with
// the location foreground service, job offers, the active job and earnings.
//
// Shipped ONLY by :app-rider. The root moduleGraphCheck forbids :app from
// reaching this module, and forbids this module from reaching :core:facear.
// No :feature:* edge: sign-in comes from :feature:auth, wired in :app-rider.
//
// DUPLICATION TO LIFT (founder decision, not done here): kyc/ (normalizeAscii,
// IFSC, account number), kycui/ (BankAccountForm, DocumentUploadField), the
// upload helper, the small card/pill kit and RupeeFormat are copies of
// :feature:kitchen's. They belong in a :core:kyc-ui module once both apps are
// stable; they were copied rather than lifted so A4 did not reshape A3.
dependencies {
    implementation(projects.core.model)
    implementation(projects.core.common)
    implementation(projects.core.designsystem)
    // food-service DTOs, RiderRepository, the scoped realtime token, Paise.
    implementation(projects.core.food)
    // The SSE client for the rider's offer stream.
    implementation(projects.core.realtime)
    // MediaUploader for DL / RC photos and the selfie.
    implementation(projects.core.media)
    // Sign-out, and the session the location service watches.
    implementation(projects.core.auth)
    // The rider_on_duty channel id for the location service's notification.
    implementation(projects.core.notifications)

    // Fused location for the on-duty pings. maps-compose is not in the offline
    // cache; navigation is handed off to Google Maps by intent instead.
    implementation(libs.play.services.location)

    // The selfie camera: CameraX core only, 1.4.1 — the version Banuba Face AR
    // pins in :app, so a rider never shifts try-on's camera stack. camera-view
    // (PreviewView) is NOT in the cache; the preview is a TextureView fed by a
    // Preview.SurfaceProvider (selfie/SelfieCamera.kt).
    implementation(libs.androidx.camera.core)
    implementation(libs.androidx.camera.camera2)
    implementation(libs.androidx.camera.lifecycle)

    implementation(libs.androidx.core.ktx)
    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.hilt.navigation.compose)
    implementation(libs.kotlinx.serialization.json)
    implementation(libs.kotlinx.coroutines.android)

    testImplementation(projects.core.testing)
}

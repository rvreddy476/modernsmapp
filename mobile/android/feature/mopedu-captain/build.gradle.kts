plugins {
    id("us.android.library")
    id("us.android.compose")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.feature.mopedu.captain"
}

// Mopedu Captain — the driver's screens (2026-09-18, ported from the Gemini
// branch): onboarding (profile, vehicle, documents, subscription, status),
// home with online/offline behind the prominent location disclosure, the
// offer card with its countdown, en route, the OTP, the trip, collecting
// payment (cash confirmed here; UPI/card waited on until the server says
// paid) and earnings. Going online starts CaptainLocationService, a
// foreground service of type location on the captain_on_duty channel, copied
// from :feature:rider's pattern; no background-location permission is ever
// requested.
//
// Shipped ONLY by :app-captain. The root moduleGraphCheck (rules b, d, k)
// forbids :app and the Feast apps from reaching this module and forbids it
// from reaching :core:facear. The captain takes no RIDE money on the device
// (cash is confirmed, UPI/card waited on), but they DO pay their own
// subscription here (2026-09-18): plans check out through payments-service
// and the sheet opens through :core:payments, stamped "mopedu", with "paid"
// read only from `GET /subscriptions/me/payment`. :app-captain is exempt from
// rule (e) for exactly this.
dependencies {
    api(projects.core.mobilityModel)
    implementation(projects.core.model)
    implementation(projects.core.common)
    implementation(projects.core.designsystem)
    implementation(projects.core.network)
    // Sign-out, and the session the location service watches.
    implementation(projects.core.auth)
    // The captain_on_duty channel id for the location service's notification.
    implementation(projects.core.notifications)
    // The subscription payment: PaymentCoordinator, PaymentHandoff, InFlightPayment.
    implementation(projects.core.payments)
    // MediaUploader (reserve → presigned PUT → confirm) for the selfie and the
    // DL / RC photos, so the server's automatic selfie check has a media id
    // to compare. The same edge :feature:rider has; :core:media reaches none
    // of what rule (c) bans.
    implementation(projects.core.media)

    // Fused location for the on-duty pings. No map library ships.
    implementation(libs.play.services.location)

    // The selfie camera: CameraX core only, 1.4.1 — the version Banuba Face AR
    // pins in :app, so nothing here shifts try-on's camera stack. camera-view
    // (PreviewView) is NOT in the cache; the preview is a TextureView fed by a
    // Preview.SurfaceProvider (selfie/SelfieCamera.kt), copied from :feature:rider.
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

plugins {
    id("us.android.library")
    id("us.android.compose")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.feature.doorsteppro"
}

// Doorstep Pro (2026-10-04): the PROFESSIONAL's screens for Doorstep home
// services — apply, the onboarding checklist from the server's readiness
// (profile photo, DigiLocker, selfie face match, skills and trade
// certificates, service area, weekly hours and days off, bank, police
// clearance certificate, agreement, PAN), duty with the location foreground
// service, offers with a countdown, jobs for a date, the visit flow (en route,
// arrived, start OTP with before photos, extras, finish, after photos, end OTP,
// no-show, cancel, unsafe exit, SOS, rating the customer), chat and earnings.
//
// Shipped ONLY by :app-doorstep-pro. The root moduleGraphCheck (rule m) keeps
// this module out of every other app and off every other :feature:*, and keeps
// the pro app free of payments, Banuba, the creator engine, posting, commerce,
// Feast, Dating, Mopedu and the customer's :feature:doorstep. The doorstep-service
// DTOs live in data/ (the Mopedu shape); the customer feature's DoorstepCall,
// Paise, screen kit, formatting, realtime token source and location flow are
// COPIED here, because features may not share code. Lift them into :core when
// a third product needs them.
dependencies {
    implementation(projects.core.model)
    implementation(projects.core.common)
    implementation(projects.core.designsystem)
    // ApiEnvelope, the shared Retrofit client, the platform Json.
    implementation(projects.core.network)
    // The SSE client for doorstep.pro.<user_id> (offers, job changes).
    implementation(projects.core.realtime)
    // MediaUploader for the profile photo, the selfie, certificates and job photos.
    implementation(projects.core.media)
    // Sign-out, and the session the location service watches.
    implementation(projects.core.auth)
    // The doorstep_pro_on_duty channel id for the location service's notification.
    implementation(projects.core.notifications)

    // Fused location for the home point and the on-duty pings. maps-compose is
    // not in the offline cache; navigation is handed to Google Maps by intent.
    implementation(libs.play.services.location)

    // The selfie camera: CameraX core only, 1.4.1 — the front lens through a
    // TextureView (camera-view is not in the cache; Feast Rider's approach).
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
    testImplementation(libs.kotlinx.coroutines.test)
}

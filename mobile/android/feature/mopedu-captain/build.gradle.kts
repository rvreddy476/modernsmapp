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
// forbids :app and the Feast apps from reaching this module, forbids it from
// reaching :core:facear, and — through rule (e) on :app-captain — keeps
// :core:payments out: the captain takes no online payment on the device.
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

    // Fused location for the on-duty pings. No map library ships.
    implementation(libs.play.services.location)

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

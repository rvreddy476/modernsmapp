plugins {
    id("us.android.library")
    id("us.android.compose")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.feature.doorstep"
}

// Doorstep inside Momentum (2026-10-04): the CUSTOMER's home-services flow —
// catalogue, service options and add-ons, addresses, the slot picker with the
// professional's 10-minute hold, checkout paid through :core:payments as
// application "doorstep", bookings with live status, extras approval and
// payment, rating, rework, cancel with the fee shown first, reschedule, SOS
// and share, and the outstanding-dues block.
//
// Depended on ONLY by :app. The root moduleGraphCheck (rule l) forbids every
// other module — the partner apps, the professionals' app, any core module —
// from reaching this one, and forbids this module from reaching any other
// :feature:*. The doorstep-service DTOs live in data/ (the Mopedu shape):
// nothing outside this feature reads them.
dependencies {
    implementation(projects.core.common)
    implementation(projects.core.designsystem)
    // ApiEnvelope, the shared Retrofit client, the platform Json.
    implementation(projects.core.network)
    // The payment sheet, the confirm-by-polling coordinator, the in-flight
    // record and the handoff bus — shared with MStore, Feast, Dating, Mopedu.
    implementation(projects.core.payments)
    // The SSE client for doorstep.booking.<id>, with Doorstep's token source.
    implementation(projects.core.realtime)

    // One-shot fused location for "Use my current location" on an address.
    // maps-compose and Places are not in the offline cache: the platform
    // Geocoder and typed fields instead (Feast's address flow, copied).
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
    testImplementation(libs.kotlinx.coroutines.test)
}

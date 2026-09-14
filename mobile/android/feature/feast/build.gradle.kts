plugins {
    id("us.android.library")
    id("us.android.compose")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.feature.feast"
}

// Feast inside Momentum (A5, 2026-09-15): the CUSTOMER's food ordering —
// restaurants, menu, cart with the server's taxes and charges, addresses,
// UPI/card payment confirmed by the server, live tracking, history, invoice.
//
// Depended on ONLY by :app. The root moduleGraphCheck forbids the partner apps
// from reaching this module (they carry no payment SDK and no customer code),
// and forbids this module from reaching :feature:commerce, :feature:kitchen or
// :feature:rider — directly or through a core module.
dependencies {
    implementation(projects.core.model)
    implementation(projects.core.common)
    implementation(projects.core.designsystem)
    // food-service customer DTOs, FeastRepository, Paise and ₹ text, the
    // scoped realtime token source.
    implementation(projects.core.food)
    // The SSE client for the order's live topic.
    implementation(projects.core.realtime)
    // The payment sheet, the confirm-by-polling coordinator, the in-flight
    // record and the handoff bus — shared with MStore, scoped to "feast".
    implementation(projects.core.payments)

    // Fused location for "Use my current location" on a delivery address.
    // maps-compose and Places are not in the offline cache: no map, no
    // autocomplete — the platform Geocoder and typed fields instead.
    implementation(libs.play.services.location)

    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.hilt.navigation.compose)
    implementation(libs.kotlinx.serialization.json)
    implementation(libs.kotlinx.coroutines.android)

    testImplementation(projects.core.testing)
    testImplementation(libs.kotlinx.coroutines.test)
}

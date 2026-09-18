plugins {
    id("us.android.library")
    id("us.android.compose")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.feature.mopedu.rider"
}

// Mopedu — the customer's ride flow inside Momentum (2026-09-18, ported from
// the Gemini branch): pickup and drop, the quote with surge, coupons and the
// fare breakdown, the payment method chosen before booking, searching,
// assigned, arrived with the OTP, the trip, the receipt, paying UPI/card
// through :core:payments as application "mopedu", cash confirmed by the
// captain, cancelling with the fee rule shown first, and the outstanding
// cancellation fees sheet.
//
// Depended on ONLY by :app. The root moduleGraphCheck (rule k) forbids every
// partner app — including :app-captain — from reaching this module, and
// forbids this module from reaching any other :feature:*.
dependencies {
    api(projects.core.mobilityModel)
    implementation(projects.core.common)
    implementation(projects.core.designsystem)
    // ApiEnvelope, the shared Retrofit client, the platform Json.
    implementation(projects.core.network)
    // The payment sheet, the confirm-by-polling coordinator, the in-flight
    // record and the handoff bus — shared with MStore, Feast and Dating.
    implementation(projects.core.payments)

    // One-shot fused location for the pickup (the Dating pattern). No map
    // library: maps-compose is not in the offline cache.
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

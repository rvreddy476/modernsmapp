plugins {
    id("us.android.library")
    id("us.android.compose")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.feature.premium"
}

// Momentum Premium (2026-11-13): buy a pass on Android, and the buyer's tax
// invoices and credit notes. monetization-service's /v1/monetization/premium
// routes; the money moves through payments-service only, as application
// "premium" on :core:payments (the app never confirms a payment, it polls the
// pass). Prices exclude GST: every number on screen is the server's.
//
// Built dark: nothing here is reachable unless :app's PremiumPurchaseSwitch is
// on (a build flag, true in debug builds, false in release until Google Play's
// payments policy for an in-app digital pass has been checked) AND
// GET premium/plans answers 200.
//
// Depended on ONLY by :app; it reaches no other :feature:*. The Settings row
// and Echo's "Get Premium" button are routes :app wires.
dependencies {
    implementation(projects.core.model)
    implementation(projects.core.common)
    implementation(projects.core.designsystem)
    implementation(projects.core.ui)
    // ApiEnvelope, apiCall, ErrorMapper and the app-wide (authenticated) Retrofit.
    implementation(projects.core.network)
    // The payment sheet, the confirm-by-polling coordinator, the in-flight
    // record and the handoff bus, scoped to "premium".
    implementation(projects.core.payments)

    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.hilt.navigation.compose)
    implementation(libs.kotlinx.serialization.json)

    testImplementation(projects.core.testing)
    testImplementation(libs.okhttp.mockwebserver)
    testImplementation(libs.retrofit.kotlinx.serialization)
}

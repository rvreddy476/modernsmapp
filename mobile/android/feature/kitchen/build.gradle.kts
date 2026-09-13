plugins {
    id("us.android.library")
    id("us.android.compose")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.feature.kitchen"
}

// Feast Kitchen's screens (A3, 2026-09-13): the role gate, restaurant
// onboarding, the menu editor, the live order queue and earnings.
//
// Shipped ONLY by :app-kitchen. The root moduleGraphCheck forbids :app from
// reaching this module, and forbids this module from reaching :core:facear.
// There is no :feature:* edge here either: sign-in comes from :feature:auth,
// wired in :app-kitchen.
dependencies {
    implementation(projects.core.model)
    implementation(projects.core.common)
    implementation(projects.core.designsystem)
    // food-service DTOs, the onboarding + kitchen repositories, Paise.
    implementation(projects.core.food)
    // The SSE client and the RealtimeTokenSource port for the live queue.
    implementation(projects.core.realtime)
    // MediaUploader for the FSSAI licence photo. Not :core:creator-model —
    // moduleGraphCheck G-6 forbids a feature holding both.
    implementation(projects.core.media)
    // Sign-out from the Restaurant tab and the not-a-partner screen.
    implementation(projects.core.auth)

    // Fused location for "Use my current location". The ONLY location
    // dependency: maps-compose and Places are not in the offline cache, so the
    // map is a MapPreview seam and the address is typed.
    implementation(libs.play.services.location)

    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.hilt.navigation.compose)
    implementation(libs.kotlinx.serialization.json)
    implementation(libs.kotlinx.coroutines.android)

    testImplementation(projects.core.testing)
}

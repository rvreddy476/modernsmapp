plugins {
    id("us.android.library")
    id("us.android.compose")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.feature.memberships"
}

// Paid memberships (2026-10-23) — the screens: the creator's Memberships page
// (eligibility in plain words, levels to create, edit and turn off) and the
// member's own list and detail (renew, change level, cancel). The data, the
// payment flow and the Join sheet live in :core:memberships.
//
// Depended on ONLY by :app (moduleGraphCheck rule (n)); it reaches no other
// :feature:*. The drawer rows that open it are routes :app wires.
dependencies {
    implementation(projects.core.model)
    implementation(projects.core.common)
    implementation(projects.core.designsystem)
    implementation(projects.core.ui)
    implementation(projects.core.memberships)
    // The creators' names on the member's list (identity-profile).
    implementation(projects.core.profile)
    // The signed-in creator's id, for their own eligibility.
    implementation(projects.core.auth)

    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.hilt.navigation.compose)
    implementation(libs.kotlinx.serialization.json)

    testImplementation(projects.core.testing)
}

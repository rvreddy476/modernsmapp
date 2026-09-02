plugins {
    id("us.android.library")
    id("us.android.compose")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.feature.settings"
}

// The module picker: first-login onboarding and its settings-hub twin.
//
// Data comes from :core:profile (ModulePreferencesRepository). It must NOT
// depend on :feature:profile: the hub row that opens the picker is wired by
// :app through a callback, which is what keeps features independent.
dependencies {
    implementation(projects.core.model)
    implementation(projects.core.common)
    implementation(projects.core.designsystem)
    implementation(projects.core.ui)
    implementation(projects.core.profile)

    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.hilt.navigation.compose)
    implementation(libs.kotlinx.serialization.json)

    testImplementation(projects.core.testing)
    // The fake-API test builds the repository against the real ErrorMapper.
    testImplementation(projects.core.network)
}

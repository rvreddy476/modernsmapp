plugins {
    id("us.android.library")
    id("us.android.compose")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.feature.settings"
}

// The module picker (first-login onboarding and its settings-hub twin), plus
// the launch-safety settings pages: Manage account, Screen time and Content
// preferences.
//
// Data comes from :core:profile and :core:auth. It must NOT depend on
// :feature:profile: the hub rows that open these pages are wired by :app
// through callbacks, which is what keeps features independent.
dependencies {
    implementation(projects.core.model)
    implementation(projects.core.common)
    implementation(projects.core.designsystem)
    implementation(projects.core.ui)
    implementation(projects.core.profile)
    // Account control: deactivate / delete live on the auth repository so the
    // session is cleared by the same code sign-out uses.
    implementation(projects.core.auth)
    // Screen time reads the local usage ledger for "today so far".
    implementation(projects.core.datastore)

    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.hilt.navigation.compose)
    implementation(libs.kotlinx.serialization.json)

    testImplementation(projects.core.testing)
    // The fake-API test builds the repository against the real ErrorMapper.
    testImplementation(projects.core.network)
    // AccountControlViewModelTest builds a real AuthRepository (SessionManager
    // included) against MockWebServer, the same way :core:auth's own tests do —
    // deactivate/delete must be proven to clear the session, not just mocked.
    testImplementation(libs.okhttp.mockwebserver)
    testImplementation(libs.retrofit.kotlinx.serialization)
}

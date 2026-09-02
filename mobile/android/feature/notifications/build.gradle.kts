plugins {
    id("us.android.library")
    id("us.android.compose")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.feature.notifications"
}

// The notification inbox.
//
// It depends on :core:notifications for data. It must NOT depend on
// :feature:post or :feature:profile: tapping a notification is handed back to
// :app as a target, and :app decides which destination that is. That is the
// same contract the composer uses for `onPublished`, and it is what keeps
// features independent.
dependencies {
    implementation(projects.core.model)
    implementation(projects.core.common)
    implementation(projects.core.designsystem)
    implementation(projects.core.ui)
    implementation(projects.core.network)
    implementation(projects.core.notifications)
    // Inline row actions: Accept / Decline / Block a message request, follow
    // back. The data layers only; the inbox still never sees another feature.
    implementation(projects.core.chat)
    implementation(projects.core.profile)
    implementation(projects.core.auth)
    // The "have we asked for the notification permission" flag (D-D2). The
    // platform cannot tell us; see NotificationPermissionPolicy.
    implementation(projects.core.datastore)

    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.navigation.compose)
    // rememberLauncherForActivityResult for the runtime permission request.
    implementation(libs.androidx.activity.compose)
    implementation(libs.hilt.navigation.compose)
    implementation(libs.kotlinx.serialization.json)

    testImplementation(projects.core.testing)
    testImplementation(libs.okhttp.mockwebserver)
    testImplementation(libs.retrofit.kotlinx.serialization)
    testImplementation(libs.robolectric)
    testImplementation(libs.kotlinx.coroutines.test)

    // Compose + navigation testing on the UNIT source set (Robolectric), so the
    // journey runs on the same `testDebugUnitTest` the gate already executes.
    testImplementation(platform(libs.compose.bom))
    testImplementation(libs.compose.ui.test.junit4)
    debugImplementation(libs.compose.ui.test.manifest)
}

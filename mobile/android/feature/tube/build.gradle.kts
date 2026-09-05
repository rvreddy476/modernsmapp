plugins {
    id("us.android.library")
    id("us.android.compose")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.feature.tube"
}

// Tube — long video (founder, 2026-09-05): the video list and the watch
// screen. Reads the `videos` surface through :core:feed, plays through
// :core:media's authenticated data source, saves watch progress through its
// own small post-service seam. Posting a video is :feature:post's pipeline;
// this module only shows the pending item the tracker reports.
dependencies {
    implementation(projects.core.model)
    implementation(projects.core.common)
    implementation(projects.core.designsystem)
    implementation(projects.core.ui)
    implementation(projects.core.network)
    implementation(projects.core.media)
    // The feed data seam: paging, hydration, the follow graph, and the shared
    // more / comments sheets.
    implementation(projects.core.feed)
    implementation(projects.core.engagement)
    // The You page's own name and avatar.
    implementation(projects.core.profile)

    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.hilt.navigation.compose)
    // BackHandler (fullscreen), and the Activity for orientation.
    implementation(libs.androidx.activity.compose)
    // WindowCompat: the system bars go away in fullscreen.
    implementation(libs.androidx.core.ktx)
    implementation(libs.coil.compose)
    implementation(libs.kotlinx.serialization.json)
    implementation(libs.paging.compose)
    implementation(libs.media3.ui.compose)

    testImplementation(projects.core.testing)
}

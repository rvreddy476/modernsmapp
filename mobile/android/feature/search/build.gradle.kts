plugins {
    id("us.android.library")
    id("us.android.compose")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.feature.search"
}

// Search (founder, 2026-09-05): one page, scoped by where it was opened
// from — Home, Reels, the video app, or Explore — over search-service's
// users and posts search and post-service's channel search. It hands every
// tap back to :app as an id (a profile, a post, a reel, a video, a channel)
// and never sees another feature, the contract every feature keeps.
dependencies {
    implementation(projects.core.model)
    implementation(projects.core.common)
    implementation(projects.core.designsystem)
    implementation(projects.core.ui)
    implementation(projects.core.network)
    // The follow graph behind the user row's Follow, and the channel DTO.
    implementation(projects.core.feed)
    // ReelsEntry: a tapped reel is left here for the Reels tab to open on.
    implementation(projects.core.media)
    // The recent-searches list lives in its own preferences file.
    implementation(libs.datastore.preferences)

    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.hilt.navigation.compose)
    implementation(libs.coil.compose)
    implementation(libs.kotlinx.serialization.json)

    testImplementation(projects.core.testing)
}

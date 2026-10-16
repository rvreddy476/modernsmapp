plugins {
    id("us.android.library")
    id("us.android.compose")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.feature.search"
}

// Search (founder, 2026-09-05; parity items T-15/T-16, 2026-10-15): one
// page, its tabs set by where it was opened from — Home, Reels, the video
// app, or Explore — over search-service's ranked, people, posts and hashtag
// searches, media-service's sound search and post-service's channel search,
// with search-service's history and popular searches for the empty box. It
// hands every tap back to :app as an id (a profile, a post, a reel, a video,
// a channel, a tag, a sound) and never sees another feature, the contract
// every feature keeps.
dependencies {
    implementation(projects.core.model)
    implementation(projects.core.common)
    implementation(projects.core.designsystem)
    implementation(projects.core.ui)
    implementation(projects.core.network)
    // Signed in or not: recents exist only for a signed-in viewer.
    implementation(projects.core.auth)
    // The follow graph behind the user row's Follow, the channel and sound DTOs.
    implementation(projects.core.feed)
    // ReelsEntry: a tapped reel is left here for the Reels tab to open on.
    implementation(projects.core.media)

    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.hilt.navigation.compose)
    implementation(libs.coil.compose)
    implementation(libs.kotlinx.serialization.json)

    testImplementation(projects.core.testing)
    // The request-mapping tests run the real Retrofit interface against
    // MockWebServer; the converter is `implementation` in :core:network.
    testImplementation(libs.okhttp.mockwebserver)
    testImplementation(libs.retrofit.kotlinx.serialization)
}

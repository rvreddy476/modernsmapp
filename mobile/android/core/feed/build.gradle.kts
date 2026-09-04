plugins {
    id("us.android.library")
    id("us.android.compose")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.core.feed"
}

// :core:feed is the feed DATA seam — feed-service's endpoints, the wire
// types, the cursor-paged source, the follow graph, the hydrators — plus the
// two ViewModel-backed sheets every surface that shows a post opens (the
// post "more" sheet and comments). It lived inside :feature:feed until Tube
// (long video) arrived as its own feature: features must not depend on each
// other, and duplicating the paging source and the DTO mapping in a second
// feature is how two screens start disagreeing about the same row. The
// same shape as :core:engagement and :core:chat.
dependencies {
    implementation(projects.core.model)
    implementation(projects.core.common)
    implementation(projects.core.designsystem)
    implementation(projects.core.ui)
    implementation(projects.core.network)
    // Playback selection (`playbackFor`) and the media delivery hydrator.
    implementation(projects.core.media)
    // Bare post-service rows are hydrated through the shared profile
    // repository; the follow graph writes through it too.
    implementation(projects.core.profile)
    // FollowGraph needs the signed-in user id.
    implementation(projects.core.auth)
    implementation(projects.core.engagement)

    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.hilt.navigation.compose)
    implementation(libs.kotlinx.serialization.json)
    // Paging is part of this module's public surface: FeedRepository returns
    // Flow<PagingData<FeedItem>>, so consumers need the type.
    api(libs.paging.runtime)

    testImplementation(projects.core.testing)
    testImplementation(libs.okhttp.mockwebserver)
    testImplementation(libs.retrofit.kotlinx.serialization)
    testImplementation(libs.paging.testing)
}

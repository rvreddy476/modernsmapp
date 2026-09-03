plugins {
    id("us.android.library")
    id("us.android.compose")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.feature.feed"
}

dependencies {
    implementation(projects.core.model)
    implementation(projects.core.common)
    implementation(projects.core.designsystem)
    implementation(projects.core.ui)
    implementation(projects.core.network)
    // Reels lives in this module as a second feed surface (see ui/reels). It
    // shares the whole data layer with home, so a separate Gradle module would
    // be fragmentation without reuse.
    implementation(projects.core.media)
    // Posts-by-hashtag arrive from post-service without an embedded author,
    // so the feed resolves one per row through the shared profile repository.
    // A core module: no feature-to-feature edge.
    implementation(projects.core.profile)
    implementation(projects.core.engagement)
    // Slice D: the top-bar badge observes the SHARED unread count so the feed
    // and the inbox can never disagree about it. A core module, so no
    // feature-to-feature edge is created.
    implementation(projects.core.notifications)
    // Muted keywords: the last server-confirmed list, applied to every page as
    // a client-side fallback. A core module, so no feature-to-feature edge.
    implementation(projects.core.datastore)

    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.hilt.navigation.compose)
    implementation(libs.kotlinx.serialization.json)
    // Paging is part of this module's public surface: FeedRepository returns
    // Flow<PagingData<FeedItem>>, so consumers need the type.
    api(libs.paging.runtime)
    implementation(libs.media3.ui.compose)
    implementation(libs.paging.compose)

    testImplementation(projects.core.testing)
    testImplementation(libs.okhttp.mockwebserver)
    testImplementation(libs.retrofit.kotlinx.serialization)
    testImplementation(libs.paging.testing)
}

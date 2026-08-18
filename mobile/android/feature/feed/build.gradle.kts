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

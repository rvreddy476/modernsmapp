plugins {
    id("us.android.library")
    id("us.android.hilt")
}

android {
    namespace = "com.us.android.core.media"
}

// :core:media owns one technical capability — video playback — and nothing
// about the product. It knows how to resolve a media URL, pool players and
// cache segments; it does not know what a reel or a post is.
dependencies {
    implementation(projects.core.common)
    // For ApiConfig (base URL) and the authenticated OkHttp client. Media3
    // MUST read through that client: HLS playlists are served authorized from
    // the gateway, and a second HTTP stack would fork token refresh.
    implementation(projects.core.network)

    api(libs.media3.exoplayer)
    api(libs.media3.exoplayer.hls)
    implementation(libs.media3.datasource.okhttp)
    implementation(libs.retrofit)
    implementation(libs.kotlinx.serialization.json)

    testImplementation(libs.junit)
    testImplementation(libs.truth)
}

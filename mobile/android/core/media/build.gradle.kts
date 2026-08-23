plugins {
    id("us.android.library")
    id("us.android.hilt")
    // Required, not optional: this module declares @Serializable DTOs for the
    // media delivery endpoint. Without the compiler plugin the annotation
    // still COMPILES and no serializer is generated, so the failure is a
    // runtime SerializationException on first parse — which is exactly how it
    // was found, silently, on a device.
    alias(libs.plugins.kotlin.serialization)
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
    // The presigned PUT is raw OkHttp on the BARE client, not Retrofit.
    implementation(libs.okhttp)
    implementation(libs.kotlinx.serialization.json)

    testImplementation(libs.junit)
    testImplementation(libs.truth)
    testImplementation(libs.kotlinx.coroutines.test)
    testImplementation(libs.okhttp.mockwebserver)
    // The converter is `implementation` in :core:network by design; wire tests
    // build their own Retrofit against MockWebServer and need it explicitly.
    testImplementation(libs.retrofit.kotlinx.serialization)
}

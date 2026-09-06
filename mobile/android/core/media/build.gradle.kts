plugins {
    id("us.android.library")
    id("us.android.hilt")
    // For ui/VideoLoading.kt — the ONE buffering indicator every video
    // surface shares. It lives here because the thing it observes is a
    // Player, and a copy per feature is how five surfaces end up with five
    // different answers to "is it stuck?" (which is what they had: none).
    id("us.android.compose")
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
    // Implements the RenderExporter port DECLARED in :core:creator-model.
    // Never :core:creator-engine — guard G-5. App DI binds this adapter.
    implementation(projects.core.creatorModel)
    implementation(projects.core.common)
    // Theme tokens for the buffering indicator (ember ring, spacing). The
    // design system owns no media, so this edge only goes one way.
    implementation(projects.core.designsystem)
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

    testImplementation(projects.core.testing)
    testImplementation(libs.junit)
    testImplementation(libs.truth)
    testImplementation(libs.kotlinx.coroutines.test)
    testImplementation(libs.okhttp.mockwebserver)
    // The converter is `implementation` in :core:network by design; wire tests
    // build their own Retrofit against MockWebServer and need it explicitly.
    testImplementation(libs.retrofit.kotlinx.serialization)
}

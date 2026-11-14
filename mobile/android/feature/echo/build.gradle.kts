plugins {
    id("us.android.library")
    id("us.android.compose")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.feature.echo"
    // The composer's GIF button (14 Nov 2026) uses chat's build-time Giphy
    // key: CHAT_GIF_API_KEY_ANDROID (or the founder's spelling
    // CHAT_GIF_API_KEY_ANDRIOD). Absent means "": the button is not drawn and
    // Giphy is never configured. Read as a tracked provider, never logged.
    buildFeatures.buildConfig = true
    val giphyKey = providers.environmentVariable("CHAT_GIF_API_KEY_ANDROID")
        .orElse(providers.environmentVariable("CHAT_GIF_API_KEY_ANDRIOD"))
        .orElse("")
        .get()
        .trim()
    defaultConfig.buildConfigField(
        "String",
        "GIPHY_API_KEY",
        "\"" + giphyKey.replace("\\", "\\\\").replace("\"", "\\\"") + "\"",
    )
}

// Echo — the premium short-posts app (2026-11-12, docs/designs/echo-contract.md):
// the two timelines, a note and its conversation, the composer (threads,
// quotes, replies, edits), the profile's Echo tab. Its own data seam over
// post-service `/v1/notes` and feed-service `/v1/feed/notes`; likes, reposts
// and reports reuse :core:engagement, photo uploads :core:media.
//
// Depended on ONLY by :app; it reaches no other :feature:*. The product
// switch (Product.ECHO) is read by :app, which decides whether any way in
// is drawn at all.
dependencies {
    implementation(projects.core.model)
    implementation(projects.core.common)
    implementation(projects.core.designsystem)
    implementation(projects.core.ui)
    implementation(projects.core.network)
    // Who is signed in: the author menu (Edit, Delete) is the viewer's own notes only.
    implementation(projects.core.auth)
    // Heart and plain repost (POST/DELETE /v1/posts/:id/...), and the report flow.
    implementation(projects.core.engagement)
    // Photo uploads (reserve → PUT → confirm → ready) and media delivery URLs.
    implementation(projects.core.media)
    // Echo v2 (14 Nov 2026): profile headers, Follow / Mute / Block from the card menu.
    implementation(projects.core.profile)
    // "Send via Chat" from the share sheet: the inbox to pick from and the post card.
    implementation(projects.core.chat)
    // The Notifications tab (All / Mentions) and its badge.
    implementation(projects.core.notifications)

    implementation(libs.androidx.activity.compose)
    // A downloaded GIF goes to :core:media as a content:// URI (FileProvider).
    implementation(libs.androidx.core.ktx)
    // The composer's GIF picker: Giphy's grid, as in chat.
    implementation(libs.giphy.ui)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.hilt.navigation.compose)
    implementation(libs.coil.compose)
    implementation(libs.kotlinx.serialization.json)

    testImplementation(projects.core.testing)
}

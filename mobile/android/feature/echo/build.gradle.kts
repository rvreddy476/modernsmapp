plugins {
    id("us.android.library")
    id("us.android.compose")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.feature.echo"
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

    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.hilt.navigation.compose)
    implementation(libs.coil.compose)
    implementation(libs.kotlinx.serialization.json)

    testImplementation(projects.core.testing)
}

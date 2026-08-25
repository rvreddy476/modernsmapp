plugins {
    id("us.android.library")
    id("us.android.compose")
}

android {
    namespace = "com.us.android.core.ui"
}

// :core:ui holds product-neutral COMPOSITES — components assembled from
// design-system primitives that more than one feature needs.
//
// The line against :core:designsystem is deliberate. designsystem owns
// foundations and primitives (theme, tokens, button, field, avatar). This
// module owns the next level up: empty/error states, stat rows, user rows.
//
// It must never gain a repository, network or Hilt dependency. Components here
// receive immutable state and callbacks; a component that can fetch is a
// component that cannot be previewed, screenshot-tested, or reused by a
// feature whose data comes from somewhere else.
dependencies {
    api(projects.core.designsystem)
    implementation(projects.core.common)
    // State TYPES only (CommentsUiState, CommentRow) — plain data classes.
    // The rule this module states is that a component here must not be able
    // to fetch; CommentsPanel receives state and callbacks and cannot.
    implementation(projects.core.engagement)
    // AsyncImage only. The ImageLoader itself is configured in :app on the
    // authenticated OkHttp client — this module still owns no networking.
    implementation(libs.coil.compose)

    testImplementation(libs.junit)
    testImplementation(libs.truth)

    // Compose semantics testing on the UNIT source set (Robolectric), so it
    // runs on the same `testDebugUnitTest` the gate already executes.
    //
    // C-CLB-3 requires proof that a described image is ANNOUNCED and a
    // decorative one is not. That claim is only checkable through the semantics
    // tree — reading the source tells you a parameter is passed, not what a
    // screen reader would say.
    //
    // The BOM is repeated for this configuration: the compose convention plugin
    // adds it to `implementation` and `androidTestImplementation` only.
    testImplementation(platform(libs.compose.bom))
    testImplementation(libs.compose.ui.test.junit4)
    testImplementation(libs.robolectric)
    debugImplementation(libs.compose.ui.test.manifest)
}

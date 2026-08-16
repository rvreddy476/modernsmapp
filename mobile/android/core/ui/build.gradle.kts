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

    testImplementation(libs.junit)
    testImplementation(libs.truth)
}

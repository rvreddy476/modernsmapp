plugins {
    id("us.android.library")
    id("us.android.compose")
    id("us.android.hilt")
    // REQUIRED, and its absence does not fail COMPILATION.
    //
    // Navigation Compose's type-safe routes are `@Serializable` objects. The
    // annotation resolves without this plugin, so the module still compiles and
    // `moduleGraphCheck` stays clean — and the app then dies on its first frame
    // with "Serializer for class 'ChatInboxRoute' is not found", because
    // `composable<ChatInboxRoute>` looks the serializer up at RUNTIME. Every
    // other feature module with a route declares this for the same reason.
    //
    // That used to be caught by nothing. `ChatNavigationTest` now resolves the
    // same serializers in a Robolectric unit test, so removing this line fails
    // all five of its cases instead of shipping a crash.
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.feature.chat"
}

// Screens only.
//
// Inbox and thread LAYOUTS live here; the behaviour they render lives in
// :core:chat. This module owns no paging, no retry, no idempotency and no
// transport — copying any of those from :core:engagement or re-implementing
// them here is what the architecture decision forbids.
//
// The visual pieces come from :core:designsystem (avatar, text field,
// scaffold, top bar) and :core:ui (loading/empty/error states), which is where
// comments and chat are allowed to look alike.
dependencies {
    implementation(projects.core.common)
    implementation(projects.core.designsystem)
    implementation(projects.core.ui)
    implementation(projects.core.chat)
    implementation(projects.core.auth)
    // Production chat pass: image attachments ride the SAME media authority
    // as every other upload (ChatAttachmentUploader), and rendering an
    // attachment needs the authorized serve URL from ApiConfig.
    implementation(projects.core.media)
    implementation(projects.core.network)
    // Clearing a handled conversation's notification when its thread opens.
    implementation(projects.core.notifications)
    // Member-picker name resolution for group creation.
    implementation(projects.core.profile)
    implementation(projects.core.model)

    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.hilt.navigation.compose)
    // The system photo picker for attachments.
    implementation(libs.androidx.activity.compose)
    implementation(libs.coil.compose)
    // Chat lock: BiometricPrompt + device credential.
    implementation(libs.androidx.biometric)

    testImplementation(projects.core.testing)
    testImplementation(libs.junit)
    testImplementation(libs.truth)
    testImplementation(libs.kotlinx.coroutines.test)
    // TEST-ONLY. The send-journey test drives the real ChatThreadViewModel
    // over a real ChatStore backed by a real in-memory Room database, so
    // "exactly one outbox row" is asserted against actual rows rather than a
    // fake's bookkeeping. Production code in this module still never sees
    // Room — moduleGraphCheck inspects implementation/api only, and this
    // stays off both.
    testImplementation(projects.core.database)

    // Compose + navigation testing, on the UNIT test source set (Robolectric),
    // not androidTest.
    //
    // B-LB-4 criterion 6 asks for an automated navigation test, and the value
    // of one is that it runs on every `testDebugUnitTest` — the same command
    // the gate already runs. An instrumented test needs a device or emulator
    // attached, so in practice it would run rarely and guard nothing between
    // runs. `:core:database` already proves Robolectric works here.
    //
    // The BOM has to be repeated for this configuration: the compose
    // convention plugin adds it to `implementation` and
    // `androidTestImplementation` only, so without this the test artifacts
    // resolve with no version.
    testImplementation(platform(libs.compose.bom))
    testImplementation(libs.compose.ui.test.junit4)
    debugImplementation(libs.compose.ui.test.manifest)
}

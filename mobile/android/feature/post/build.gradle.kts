plugins {
    id("us.android.library")
    id("us.android.compose")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.feature.post"
}

dependencies {
    implementation(projects.core.model)
    implementation(projects.core.common)
    implementation(projects.core.designsystem)
    implementation(projects.core.ui)
    implementation(projects.core.network)
    implementation(projects.core.profile)
    implementation(projects.core.media)
    implementation(projects.core.feed)
    // The publish transport PORT lives in the pure contract module; this
    // adapter implements it beside the DTO whose bytes it freezes.
    implementation(projects.core.creatorModel)
    implementation(projects.core.creatorEngine)
    // The composer draft is durable: it must survive a navigation pop, which
    // SavedStateHandle does not. See ComposerDraftStore.
    implementation(projects.core.database)
    implementation(projects.core.engagement)
    // The Create sheet remembers its "Compact view" choice.
    implementation(projects.core.datastore)

    // The composer previews the picked photo as a card. The ImageLoader itself
    // is configured in :app on the authenticated client; this module renders only.
    implementation(libs.coil.compose)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.navigation.compose)
    // rememberLauncherForActivityResult for the system Photo Picker.
    implementation(libs.androidx.activity.compose)
    implementation(libs.hilt.navigation.compose)
    implementation(libs.kotlinx.serialization.json)
    // Accessible drag-and-drop for the Studio's thumbnail strip ONLY — the
    // binding library decision for P1-A. It reorders a LazyRow; every actual
    // move is still a MovePage command through the reducer.
    implementation(libs.reorderable)
    // Studio background publish continuation.
    implementation(libs.work.runtime)
    implementation(libs.hilt.work)
    ksp(libs.hilt.work.compiler)

    testImplementation(projects.core.testing)
    testImplementation(libs.okhttp.mockwebserver)

    // Compose + navigation testing on the UNIT test source set (Robolectric),
    // not androidTest.
    //
    // C-LB-7 asks for an automated journey through the REGISTERED composer
    // destination, and the value of one is that it runs on every
    // `testDebugUnitTest` — the same command the gate already runs. An
    // instrumented test needs a device or emulator attached, and a test that
    // needs a device is a test that does not run. `:feature:chat` and
    // `:core:database` already prove Robolectric works in this build.
    //
    // The Compose BOM aligns the test artifacts with the UI ones; the test
    // manifest supplies the `ComponentActivity` the journey is hosted in, which
    // is what makes a REAL system-Back dispatch possible.
    testImplementation(platform(libs.compose.bom))
    testImplementation(libs.compose.ui.test.junit4)
    debugImplementation(libs.compose.ui.test.manifest)
    // The converter is `implementation` in :core:network by design. Contract
    // tests build their own Retrofit against MockWebServer and need it here.
    testImplementation(libs.retrofit.kotlinx.serialization)

    // Hilt on the unit-test graph — C-CLB-2.
    //
    // The journey test that matters is the one through the REAL
    // `composerScreen()` registration, whose ViewModel is created and owned by
    // navigation via `hiltViewModel()`. That ownership is the thing under test:
    // popping the destination clears that ViewModel and cancels its scope, and
    // a hand-constructed ViewModel held by the test can never reproduce it.
    //
    // `kspTest` is required as well as the artifact — without the compiler on
    // the test source set the generated `HiltTestApplication` component never
    // includes the modules under test and every injection fails at runtime.
    testImplementation(libs.hilt.android.testing)
    kspTest(libs.hilt.compiler)

    // Test-only: the C-CLB-2 Hilt graph needs `TelemetryConfig`, which `:app`
    // normally supplies. A TEST dependency, so the production module graph
    // (and moduleGraphCheck) is unchanged.
    testImplementation(projects.core.telemetry)
}

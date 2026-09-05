plugins {
    id("us.android.library")
    id("us.android.compose")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.feature.commerce"
}

dependencies {
    implementation(projects.core.model)
    implementation(projects.core.common)
    implementation(projects.core.designsystem)
    implementation(projects.core.ui)
    implementation(projects.core.commerce)
    implementation(projects.core.auth)
    // Product and order imagery. LB-A3: reuse the media loader rather than
    // introducing a second image stack in this feature.
    implementation(projects.core.media)
    // The loader itself. CommerceImage was a permanent grey box until the
    // server started resolving media ids into URLs; it now draws them, using
    // the same Coil stack every other feature module uses.
    implementation(libs.coil.compose)
    // The document picker. A KYC document is often a PDF, so this is the
    // general file picker rather than the photo picker in :core:media.
    implementation(libs.androidx.activity.compose)
    implementation(projects.core.network)

    // NOTE: there is deliberately no `projects.feature.*` dependency here.
    // The rule is enforced by the `checkFeatureGraph` task in the root
    // build.gradle.kts, not by convention — see LB-A4.

    implementation(libs.androidx.lifecycle.viewmodel.compose)
    // The buyer journey is a graph of destinations, and its ViewModels are
    // scoped to those destinations — so the feature needs both navigation and
    // the hilt bridge that resolves a ViewModel from a NavBackStackEntry.
    implementation(libs.androidx.navigation.compose)
    implementation(libs.hilt.navigation.compose)
    // Type-safe routes are @Serializable data classes.
    implementation(libs.kotlinx.serialization.json)

    testImplementation(projects.core.testing)
    testImplementation(libs.kotlinx.coroutines.test)
}

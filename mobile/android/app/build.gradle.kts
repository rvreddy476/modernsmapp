plugins {
    id("us.android.application")
    id("us.android.compose")
    id("us.android.hilt")
    // Navigation Compose's type-safe routes are @Serializable objects, so
    // the serialization plugin is required even though we do no JSON here yet.
    alias(libs.plugins.kotlin.serialization)
    alias(libs.plugins.google.services)
}

android {
    namespace = "com.us.android"

    defaultConfig {
        // ⚠ IMMUTABLE once published to Play. Verified spelling: a-n-d-r-o-i-d.
        // Blocker B1, resolved 2026-08-16.
        applicationId = "com.us.android"
    }
}

dependencies {
    implementation(projects.core.common)
    implementation(projects.core.model)
    implementation(projects.core.designsystem)
    implementation(projects.core.ui)
    implementation(projects.core.media)
    implementation(projects.core.notifications)
    implementation(projects.feature.notifications)
    implementation(projects.core.network)
    implementation(projects.core.auth)
    implementation(projects.core.database)
    implementation(projects.core.creatorEngine)
    implementation(projects.core.creatorModel)
    implementation(libs.work.runtime)
    implementation(libs.hilt.work)
    ksp(libs.hilt.work.compiler)
    implementation(projects.core.datastore)
    implementation(projects.core.engagement)
    // Production chat pass: the chat lock lifecycle hooks.
    implementation(projects.core.chat)
    implementation(projects.core.call)
    implementation(projects.feature.auth)
    implementation(projects.feature.call)
    implementation(projects.feature.chat)
    implementation(projects.feature.feed)
    implementation(projects.feature.post)
    implementation(projects.feature.profile)

    implementation(libs.androidx.core.ktx)
    implementation(libs.coil.compose)
    implementation(libs.coil.network.okhttp)
    implementation(libs.androidx.core.splashscreen)
    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.androidx.lifecycle.runtime.ktx)
    implementation(libs.androidx.lifecycle.process)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.hilt.navigation.compose)
    implementation(libs.kotlinx.coroutines.android)
    implementation(libs.kotlinx.serialization.json)

    testImplementation(projects.core.testing)

    // No androidTest dependencies yet. Phase 0 ships no instrumented tests
    // (they arrive in Phase 2 with the first real screen), and declaring
    // androidx.test.ext:junit here drags concurrent-futures 1.2.0 into the
    // androidTest classpath, which conflicts with the strict 1.1.0 the
    // Compose BOM pins. Add them back in Phase 2 together with a resolution
    // strategy for that constraint.
}

plugins {
    id("us.android.application")
    id("us.android.compose")
    id("us.android.hilt")
    // Navigation Compose's type-safe routes are @Serializable objects, so
    // the serialization plugin is required even though we do no JSON here yet.
    alias(libs.plugins.kotlin.serialization)
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
    implementation(projects.core.network)
    implementation(projects.core.auth)
    implementation(projects.core.database)
    implementation(projects.core.datastore)
    implementation(projects.feature.auth)

    implementation(libs.androidx.core.ktx)
    implementation(libs.androidx.core.splashscreen)
    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.androidx.lifecycle.runtime.ktx)
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

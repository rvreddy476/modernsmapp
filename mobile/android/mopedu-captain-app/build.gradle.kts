plugins {
    id("us.android.application")
    id("us.android.compose")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.mopedu.captain"

    defaultConfig {
        applicationId = "com.us.android.mopedu.captain"
        versionCode = 1
        versionName = "1.0.0"
    }
}

dependencies {
    implementation(projects.feature.mopeduCaptain)
    implementation(projects.core.mobilityModel)
    implementation(projects.core.designsystem)
    implementation(projects.core.location)
    implementation(projects.core.network)
    implementation(projects.core.telemetry)
    implementation(projects.core.auth)
    implementation(projects.core.common)

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
    testImplementation(libs.junit)
    testImplementation(libs.truth)
}

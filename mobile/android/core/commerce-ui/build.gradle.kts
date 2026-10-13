plugins {
    id("us.android.library")
    id("us.android.compose")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}
android { namespace = "com.us.android.core.commerce.ui" }
dependencies {
    testImplementation(projects.core.testing)
    api(projects.core.commerce)
    implementation(projects.core.designsystem)
    implementation(projects.core.ui)
    implementation(projects.core.media)
    implementation(projects.core.maps)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.hilt.navigation.compose)
    implementation(libs.coil.compose)
}

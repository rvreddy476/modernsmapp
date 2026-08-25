plugins {
    id("us.android.library")
    id("us.android.compose")
}

android {
    namespace = "com.us.android.core.designsystem"
}

dependencies {
    implementation(projects.core.common)
    implementation(libs.coil.compose)

    testImplementation(libs.junit)
    testImplementation(libs.truth)
}

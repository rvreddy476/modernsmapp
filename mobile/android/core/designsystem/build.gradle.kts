plugins {
    id("us.android.library")
    id("us.android.compose")
}

android {
    namespace = "com.us.android.core.designsystem"
}

dependencies {
    implementation(projects.core.common)

    testImplementation(libs.junit)
    testImplementation(libs.truth)
}

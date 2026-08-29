plugins {
    id("us.android.library")
    id("us.android.compose")
    id("us.android.hilt")
}

android {
    namespace = "com.us.android.core.maps"
}

dependencies {
    api(projects.core.mobilityModel)
    api(projects.core.designsystem)
    api(projects.core.common)

    implementation(libs.androidx.core.ktx)
    implementation(libs.androidx.lifecycle.runtime.ktx)
    implementation(libs.kotlinx.coroutines.android)

    testImplementation(projects.core.testing)
    testImplementation(libs.junit)
    testImplementation(libs.truth)
}

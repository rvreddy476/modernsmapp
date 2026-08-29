plugins {
    id("us.android.library")
    id("us.android.hilt")
}

android {
    namespace = "com.us.android.core.location"
}

dependencies {
    api(projects.core.mobilityModel)
    api(projects.core.common)

    implementation(libs.androidx.core.ktx)
    implementation(libs.kotlinx.coroutines.android)

    testImplementation(projects.core.testing)
    testImplementation(libs.junit)
    testImplementation(libs.truth)
}

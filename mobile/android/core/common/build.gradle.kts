plugins {
    id("us.android.library")
    id("us.android.hilt")
}

android {
    namespace = "com.us.android.core.common"
}

dependencies {
    api(projects.core.model)

    implementation(libs.kotlinx.coroutines.android)

    testImplementation(libs.junit)
    testImplementation(libs.truth)
    testImplementation(libs.turbine)
    testImplementation(libs.kotlinx.coroutines.test)
}

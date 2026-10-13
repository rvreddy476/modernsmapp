plugins {
    id("us.android.library")
    id("us.android.compose")
}
android { namespace = "com.us.android.core.maps" }
dependencies {
    implementation(projects.core.designsystem)
    implementation(libs.maps.compose)
    implementation(libs.androidx.lifecycle.runtime.compose)
    testImplementation(libs.junit)
}

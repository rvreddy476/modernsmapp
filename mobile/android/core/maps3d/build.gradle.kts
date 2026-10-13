plugins {
    id("us.android.library")
    id("us.android.compose")
}
android { namespace = "com.us.android.core.maps3d" }
dependencies {
    implementation(projects.core.designsystem)
    implementation(libs.maps3d)
    implementation(libs.androidx.lifecycle.runtime.compose)
}

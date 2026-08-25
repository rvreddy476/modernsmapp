plugins {
    id("us.android.library")
    id("us.android.hilt")
}

android {
    namespace = "com.us.android.core.datastore"
}

dependencies {
    api(projects.core.model)
    implementation(projects.core.common)

    api(libs.datastore.preferences)
    implementation(libs.kotlinx.coroutines.android)

    testImplementation(projects.core.testing)
}

plugins {
    id("us.android.library")
    id("us.android.hilt")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.us.android.core.notifications"
}

// One technical capability: getting a push token and telling the server about
// it. It knows nothing about what a notification means to the product.
dependencies {
    implementation(projects.core.common)
    implementation(projects.core.network)

    implementation(platform(libs.firebase.bom))
    implementation(libs.firebase.messaging)
    implementation(libs.kotlinx.serialization.json)
    implementation(libs.kotlinx.coroutines.android)

    testImplementation(libs.junit)
    testImplementation(libs.truth)
}

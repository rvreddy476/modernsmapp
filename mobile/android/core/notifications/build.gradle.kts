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
    implementation(projects.core.model)
    implementation(projects.core.network)

    // Public ABI: UsMessagingService extends FirebaseMessagingService, so the
    // app manifest's lint analysis must see this superclass transitively.
    api(platform(libs.firebase.bom))
    api(libs.firebase.messaging)
    implementation(libs.kotlinx.serialization.json)
    implementation(libs.kotlinx.coroutines.android)

    testImplementation(libs.junit)
    testImplementation(libs.truth)

    testImplementation(projects.core.testing)
    testImplementation(libs.okhttp.mockwebserver)
    // The converter is `implementation` in :core:network by design. Contract
    // tests build their own Retrofit against MockWebServer and need it here.
    testImplementation(libs.retrofit.kotlinx.serialization)
    testImplementation(libs.kotlinx.coroutines.test)
}

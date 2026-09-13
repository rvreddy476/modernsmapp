plugins {
    id("us.android.application")
    // Deliberately NO `us.android.banuba`: Feast Kitchen ships neither the
    // Banuba SDK nor its licence token (Feast A0). The application convention
    // plugin fails the build if the licence field appears without it.
    id("us.android.compose")
    id("us.android.hilt")
    // Type-safe Navigation Compose routes are @Serializable.
    alias(libs.plugins.kotlin.serialization)
    // Resolved but NOT applied here — see the Firebase note below.
    alias(libs.plugins.google.services) apply false
}

/*
 * Feast Kitchen — the restaurant partner app (Feast A3, 2026-09-13).
 *
 * applicationId `com.us.feast.kitchen` is PROPOSED. It is immutable once
 * published to Play, so the founder confirms it before the first upload; until
 * then it is only a development install. Flavours (dev/staging/prod) and their
 * `.dev` / `.staging` suffixes come from the application convention plugin,
 * exactly as for Momentum.
 *
 * Maps: the convention plugin reads `.secrets/google-maps-android-app-kitchen.key`
 * into MAPS_API_KEY. No map library is on the classpath yet (maps-compose is
 * not in the offline cache), so the key is unused and the location step shows
 * its "Map preview unavailable" seam.
 *
 * FIREBASE: there is no google-services.json for this app. The Google Services
 * plugin fails a build without one, so it is applied only when the file exists.
 * Without it FirebaseApp never initialises, FCM is disabled, and
 * KitchenApplication logs that once. Drop the file in this directory and the
 * next build picks it up — no build-file change.
 */
if (file("google-services.json").exists()) {
    apply(plugin = "com.google.gms.google-services")
}

android {
    namespace = "com.us.feast.kitchen"

    defaultConfig {
        // PROPOSED — founder confirms before Play. See the note above.
        applicationId = "com.us.feast.kitchen"

        // Feast Kitchen's own release line, independent of Momentum's.
        versionCode = 1
        versionName = "0.1.0"
    }
}

dependencies {
    implementation(projects.core.common)
    implementation(projects.core.model)
    implementation(projects.core.designsystem)
    implementation(projects.core.network)
    implementation(projects.core.auth)
    implementation(projects.core.datastore)
    implementation(projects.core.telemetry)
    // NotificationChannelSpec.KITCHEN, and the FCM service class the Hilt graph
    // must be able to inject even while FCM is disabled.
    implementation(projects.core.notifications)
    implementation(projects.core.food)
    implementation(projects.core.realtime)
    implementation(projects.core.media)

    // Sign-in, registration and email verification: the same screens and
    // ViewModels Momentum uses, wired to this app's own graph.
    implementation(projects.feature.auth)
    implementation(projects.feature.kitchen)

    implementation(libs.androidx.core.ktx)
    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.androidx.lifecycle.runtime.ktx)
    implementation(libs.androidx.lifecycle.process)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.hilt.navigation.compose)
    implementation(libs.kotlinx.coroutines.android)
    implementation(libs.kotlinx.serialization.json)

    testImplementation(projects.core.testing)
}

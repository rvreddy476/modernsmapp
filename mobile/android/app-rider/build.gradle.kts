plugins {
    id("us.android.application")
    // Deliberately NO `us.android.banuba`: Feast Rider ships neither the Banuba
    // SDK nor its licence token (Feast A0). The application convention plugin
    // fails the build if the licence field appears without it.
    id("us.android.compose")
    id("us.android.hilt")
    // Type-safe Navigation Compose routes are @Serializable.
    alias(libs.plugins.kotlin.serialization)
    // Resolved but NOT applied here — see the Firebase note below.
    alias(libs.plugins.google.services) apply false
}

/*
 * Feast Rider — the delivery partner app (Feast A4, 2026-09-13).
 *
 * applicationId `com.us.feast.rider` is PROPOSED. It is immutable once published
 * to Play, so the founder confirms it before the first upload. Flavours
 * (dev/staging/prod) and their suffixes come from the application convention
 * plugin, exactly as for Momentum and Kitchen.
 *
 * Maps: the convention plugin reads `.secrets/google-maps-android-app-rider.key`
 * into MAPS_API_KEY. No map library ships; navigation is handed off to Google
 * Maps by intent, which needs no key.
 *
 * FIREBASE: there is no google-services.json for this app. The Google Services
 * plugin fails a build without one, so it is applied only when the file exists.
 * Without it FCM is disabled, job offers arrive through the in-app realtime
 * stream only, and RiderApplication logs that once.
 *
 * DIGILOCKER RETURN LINK: food-service's public return route 302s to
 * FOOD_RIDER_APP_LINK_URL, which it requires to be an absolute http(s) URL.
 * The activity claims `<scheme>://<riderAppLinkHost>/rider/digilocker/return`
 * (and `/rider/offers/…`) per flavour below. The hosts are PLACEHOLDERS on the
 * reserved `.invalid` TLD — they can never resolve, so a link the app does not
 * catch fails in the browser instead of reaching a stranger's server. The
 * founder picks the real staging/prod domain and serves assetlinks.json for it
 * before App Links can verify. The dev flavour also claims
 * `feastrider://digilocker/return` (src/dev/AndroidManifest.xml), which
 * food-service cannot redirect to until its checkURL allows a custom scheme.
 */
if (file("google-services.json").exists()) {
    apply(plugin = "com.google.gms.google-services")
}

android {
    namespace = "com.us.feast.rider"

    defaultConfig {
        // PROPOSED — founder confirms before Play. See the note above.
        applicationId = "com.us.feast.rider"

        // Feast Rider's own release line, independent of Momentum's and Kitchen's.
        versionCode = 1
        versionName = "0.1.0"

        manifestPlaceholders["riderAppLinkHost"] = "rider.feast.invalid"
    }

    productFlavors {
        getByName("dev") {
            manifestPlaceholders["riderAppLinkHost"] = "rider.feast.dev.invalid"
        }
        getByName("staging") {
            manifestPlaceholders["riderAppLinkHost"] = "rider.feast.staging.invalid"
        }
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
    // NotificationChannelSpec.RIDER, PushApp, and the FCM service class the Hilt
    // graph must be able to inject even while FCM is disabled.
    implementation(projects.core.notifications)
    implementation(projects.core.food)
    implementation(projects.core.realtime)
    implementation(projects.core.media)

    // Sign-in, registration and email verification: Momentum's own screens.
    implementation(projects.feature.auth)
    implementation(projects.feature.rider)

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

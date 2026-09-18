plugins {
    id("us.android.application")
    // Deliberately NO `us.android.banuba`: Mopedu Captain ships neither the
    // Banuba SDK nor its licence token. The application convention plugin
    // fails the build if the licence field appears without it.
    id("us.android.compose")
    id("us.android.hilt")
    // Type-safe Navigation Compose routes are @Serializable.
    alias(libs.plugins.kotlin.serialization)
    // Resolved but NOT applied here — see the Firebase note below.
    alias(libs.plugins.google.services) apply false
}

/*
 * Mopedu Captain — the driver app (2026-09-18, ported from the Gemini branch
 * onto the Feast Rider shape).
 *
 * applicationId `com.us.mopedu.captain` is PROPOSED. It is immutable once
 * published to Play, so the founder confirms it before the first upload.
 * Flavours (dev/staging/prod) and their suffixes come from the application
 * convention plugin, exactly as for Momentum, Kitchen and Rider.
 *
 * Maps: the convention plugin reads `.secrets/google-maps-android-app-captain.key`
 * into MAPS_API_KEY. No map library ships; the ride map is a placeholder.
 *
 * FIREBASE: there is no google-services.json for this app. The Google Services
 * plugin fails a build without one, so it is applied only when the file exists.
 * Without it FCM is disabled, offers arrive by polling only, and
 * CaptainApplication logs that once.
 */
if (file("google-services.json").exists()) {
    apply(plugin = "com.google.gms.google-services")
}

android {
    namespace = "com.us.mopedu.captain"

    defaultConfig {
        // PROPOSED — founder confirms before Play. See the note above.
        applicationId = "com.us.mopedu.captain"

        // Mopedu Captain's own release line, independent of the other apps'.
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
    // NotificationChannelSpec.CAPTAIN, PushApp, and the FCM service class the
    // Hilt graph must be able to inject even while FCM is disabled.
    implementation(projects.core.notifications)
    implementation(projects.core.mobilityModel)
    // The payment sheet (2026-09-18): the captain pays their subscription on
    // the device through payments-service, so CaptainActivity is the
    // ActivityPaymentHost exactly as Momentum's MainActivity is. The root
    // moduleGraphCheck exempts :app-captain alone from rule (e); the Feast
    // partner apps still carry no PSP SDK.
    implementation(projects.core.payments)

    // Sign-in, registration and email verification: Momentum's own screens.
    implementation(projects.feature.auth)
    implementation(projects.feature.mopeduCaptain)

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

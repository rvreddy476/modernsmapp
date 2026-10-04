plugins {
    id("us.android.application")
    // Deliberately NO `us.android.banuba`: Doorstep Pro ships neither the
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
 * Doorstep Pro — the home-service professionals' app (2026-10-04, lane L-J).
 *
 * applicationId `com.us.doorstep.pro` is PROPOSED. It is immutable once
 * published to Play, so the founder confirms it before the first upload.
 * Flavours (dev/staging/prod) and their suffixes come from the application
 * convention plugin, exactly as for Momentum, Kitchen, Rider and Captain.
 *
 * Maps: the convention plugin reads `.secrets/google-maps-android-app-doorstep-pro.key`
 * into MAPS_API_KEY (empty when absent). No map library ships; navigation to
 * the customer is handed to Google Maps by a `geo:` intent, which needs no key.
 *
 * FIREBASE: there is no google-services.json for this app yet. The Google
 * Services plugin fails a build without one, so it is applied only when the
 * file exists. Without it FCM is disabled: offers arrive through the in-app
 * realtime stream and its 15-second polling fallback, and
 * DoorstepProApplication logs that once. With it, devices register as
 * `doorstep_pro` and pushes land on the four doorstep_pro_* channels.
 *
 * DIGILOCKER RETURN LINK: doorstep-service's redirect URI is
 * DOORSTEP_PRO_APP_LINK_URL, else `<DOORSTEP_PUBLIC_BASE_URL>/doorstep-pro/digilocker`
 * (config.DigiLockerRedirectURI), and it must be an absolute http(s) URL. The
 * activity claims `https://<doorstepProAppLinkHost>/doorstep-pro/digilocker`
 * per flavour below. The hosts are PLACEHOLDERS on the reserved `.invalid` TLD
 * — they can never resolve, so a link the app does not catch fails in the
 * browser instead of reaching a stranger's server. The founder picks the real
 * staging/prod host and serves assetlinks.json for it before App Links can
 * verify; a dev build can point at the tunnel with
 * `-PdoorstepProAppLinkHost=<host>`. The dev flavour also claims
 * `doorstep-pro://digilocker` (src/dev/AndroidManifest.xml), which
 * doorstep-service cannot redirect to until its URL check allows a custom
 * scheme outside production.
 */
if (file("google-services.json").exists()) {
    apply(plugin = "com.google.gms.google-services")
}

val devAppLinkHost: String = providers.gradleProperty("doorstepProAppLinkHost").orNull?.takeIf { it.isNotBlank() }
    ?: "pro.doorstep.dev.invalid"

android {
    namespace = "com.us.doorstep.pro"

    defaultConfig {
        // PROPOSED — founder confirms before Play. See the note above.
        applicationId = "com.us.doorstep.pro"

        // Doorstep Pro's own release line, independent of the other apps'.
        versionCode = 1
        versionName = "0.1.0"

        manifestPlaceholders["doorstepProAppLinkHost"] = "pro.doorstep.invalid"
    }

    productFlavors {
        getByName("dev") {
            manifestPlaceholders["doorstepProAppLinkHost"] = devAppLinkHost
        }
        getByName("staging") {
            manifestPlaceholders["doorstepProAppLinkHost"] = "pro.doorstep.staging.invalid"
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
    // NotificationChannelSpec.DOORSTEP_PRO, PushApp.DOORSTEP_PRO, the FCM
    // service that presents pushes on those channels, and token registration.
    implementation(projects.core.notifications)
    // The SSE client the feature streams doorstep.pro.<user_id> through.
    implementation(projects.core.realtime)
    // MediaUploader for photos and certificates.
    implementation(projects.core.media)

    // NO :core:payments — a professional pays nothing on the device. The root
    // moduleGraphCheck (rule m) keeps it that way.

    // Sign-in, registration and email verification: Momentum's own screens.
    implementation(projects.feature.auth)
    implementation(projects.feature.doorstepPro)

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

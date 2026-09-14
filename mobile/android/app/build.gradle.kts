plugins {
    id("us.android.application")
    // The Banuba licence token. Momentum is the only app that ships Banuba;
    // it must come AFTER us.android.application and never be applied to a
    // Feast partner app (Feast A0, 2026-09-13).
    id("us.android.banuba")
    id("us.android.compose")
    id("us.android.hilt")
    // Navigation Compose's type-safe routes are @Serializable objects, so
    // the serialization plugin is required even though we do no JSON here yet.
    alias(libs.plugins.kotlin.serialization)
    alias(libs.plugins.google.services)
}

android {
    namespace = "com.us.android"

    defaultConfig {
        // ⚠ IMMUTABLE once published to Play. Verified spelling: a-n-d-r-o-i-d.
        // Blocker B1, resolved 2026-08-16.
        applicationId = "com.us.android"

        // Momentum's own release line. Set here, not in the convention plugin,
        // since Feast Kitchen and Feast Rider version independently; a release
        // variant without a versionCode fails the build.
        versionCode = 1
        versionName = "0.1.0"
    }

    // Banuba Video Editor SDK (2026-09-05). Two of its AARs (camera-sdk and
    // ve-sdk) ship the same libbanuba-ve-yuv.so, and its loader expects the
    // libraries extracted on disk rather than mapped from the APK — both are
    // the vendor's documented requirements, mirrored by extractNativeLibs in
    // the manifest.
    packaging {
        jniLibs {
            pickFirsts += "**/libbanuba-ve-yuv.so"
            useLegacyPackaging = true
        }
    }
}

dependencies {
    implementation(projects.core.common)
    implementation(projects.core.model)
    implementation(projects.core.designsystem)
    implementation(projects.core.ui)
    implementation(projects.core.media)
    implementation(projects.core.notifications)
    implementation(projects.feature.notifications)
    implementation(projects.core.network)
    implementation(projects.core.auth)
    implementation(projects.core.database)
    implementation(projects.core.creatorEngine)
    implementation(projects.core.creatorModel)
    implementation(libs.work.runtime)
    implementation(libs.hilt.work)
    ksp(libs.hilt.work.compiler)
    implementation(projects.core.datastore)
    implementation(projects.core.engagement)
    // Production chat pass: the chat lock lifecycle hooks.
    implementation(projects.core.chat)
    // The product-analytics client: its background flush hangs off the
    // process lifecycle observer in UsApplication.
    implementation(projects.core.analytics)
    implementation(projects.core.call)
    // The shell gates on module preferences (which tabs, which home).
    implementation(projects.core.profile)
    implementation(projects.feature.auth)
    implementation(projects.feature.call)
    implementation(projects.feature.chat)
    implementation(projects.feature.feed)
    implementation(projects.feature.tube)
    implementation(projects.feature.search)
    implementation(projects.feature.live)
    implementation(projects.feature.post)
    implementation(projects.feature.profile)
    implementation(projects.feature.settings)
    // The commerce buyer journey: catalogue, product, cart, address, checkout,
    // payment handoff and orders.
    implementation(projects.feature.commerce)
    // The payment attempt type the commerce routes carry.
    implementation(projects.core.commerce)
    // The payment sheet (2026-09-14). MainActivity is the ActivityPaymentHost
    // the PSP SDK calls back on. The provider SDK itself is declared by
    // :core:payments, not here — :app names no payment provider.
    implementation(projects.core.payments)

    implementation(libs.androidx.core.ktx)
    implementation(libs.coil.compose)
    implementation(libs.coil.network.okhttp)
    implementation(libs.androidx.core.splashscreen)
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

    // No androidTest dependencies yet. Phase 0 ships no instrumented tests
    // (they arrive in Phase 2 with the first real screen), and declaring
    // androidx.test.ext:junit here drags concurrent-futures 1.2.0 into the
    // androidTest classpath, which conflicts with the strict 1.1.0 the
    // Compose BOM pins. Add them back in Phase 2 together with a resolution
    // strategy for that constraint.
}

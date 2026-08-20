package com.us.android.convention

import com.android.build.api.dsl.ApplicationExtension
import com.android.build.api.dsl.CommonExtension
import org.gradle.api.JavaVersion
import org.gradle.api.Project
import org.gradle.api.artifacts.VersionCatalog
import org.gradle.api.artifacts.VersionCatalogsExtension
import org.gradle.api.plugins.JavaPluginExtension
import org.gradle.api.tasks.testing.Test
import org.gradle.kotlin.dsl.getByType
import org.jetbrains.kotlin.gradle.dsl.JvmTarget
import org.jetbrains.kotlin.gradle.dsl.KotlinAndroidProjectExtension
import org.jetbrains.kotlin.gradle.dsl.KotlinJvmProjectExtension

/**
 * Single place where SDK levels live. Changing minSdk after Phase 1 would
 * invalidate the perf baselines and the test matrix, so it is deliberately
 * not a per-module knob.
 *
 * minSdk 26 was chosen (blocker B3, resolved 2026-08-16) because it makes
 * notification channels native — a hard requirement for Phase 8 push — and
 * unlocks adaptive icons and java.time without desugaring.
 */
object AndroidSdk {
    const val MIN = 26

    /** Runtime behaviour opt-in. Held at 36 deliberately; raising it is a
     *  product decision about new OS behaviours, not a build cleanup. */
    const val TARGET = 36

    /** Forced to 37 by androidx.core:core-ktx 1.19, which refuses to be
     *  compiled against 36. Compiling against 37 does not change runtime
     *  behaviour — that is what TARGET controls. */
    const val COMPILE = 37
}

internal val Project.libs: VersionCatalog
    get() = extensions.getByType<VersionCatalogsExtension>().named("libs")

/**
 * Java/Kotlin settings shared by every Android module.
 *
 * Note on the JDK: source/target compatibility is pinned to 17 rather than a
 * Gradle toolchain. A toolchain forces every machine and CI runner to have
 * exactly JDK 17 present, or to auto-provision it over the network; pinning
 * the bytecode level instead lets the daemon run on 17 or 21 and still emit
 * identical output. See README "Toolchain".
 */
internal fun Project.configureKotlinAndroid(commonExtension: CommonExtension) {
    // gradle-api 9.x dropped CommonExtension's type parameters and its
    // nested-lambda DSL, so these are plain property assignments.
    commonExtension.compileSdk = AndroidSdk.COMPILE
    commonExtension.defaultConfig.minSdk = AndroidSdk.MIN
    commonExtension.compileOptions.sourceCompatibility = JavaVersion.VERSION_17
    commonExtension.compileOptions.targetCompatibility = JavaVersion.VERSION_17

    // Core library desugaring, enabled for EVERY module rather than per-module.
    //
    // The OpenTelemetry SDK uses java.time and other APIs not present on all
    // minSdk-26 devices. AGP requires the consuming application to enable
    // desugaring too, not just the library that needs it — so a per-module
    // opt-in fails the build at :app with a metadata error rather than at the
    // module that introduced the requirement. Enabling it once here keeps the
    // toolchain consistent and the failure mode away from future contributors.
    commonExtension.compileOptions.isCoreLibraryDesugaringEnabled = true
    dependencies.add(
        "coreLibraryDesugaring",
        libs.findLibrary("desugar-jdk-libs").get(),
    )

    extensions.configure(KotlinAndroidProjectExtension::class.java) {
        compilerOptions {
            jvmTarget.set(JvmTarget.JVM_17)
            allWarningsAsErrors.set(false)
            freeCompilerArgs.addAll("-opt-in=kotlin.RequiresOptIn")
        }
    }

    // Gradle 9 fails a Test task that discovers no tests. That default is a
    // good signal for a misconfigured test task, but a wrong one for a
    // multi-module scaffold where several modules legitimately have no tests
    // yet. Modules that DO have tests still fail normally when those fail.
    tasks.withType(Test::class.java).configureEach {
        failOnNoDiscoveredTests.set(false)
    }
}

internal fun Project.configureKotlinJvm() {
    extensions.configure(KotlinJvmProjectExtension::class.java) {
        compilerOptions {
            jvmTarget.set(JvmTarget.JVM_17)
        }
    }
    extensions.configure(JavaPluginExtension::class.java) {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}

/**
 * The `environment` flavor dimension.
 *
 * Applied to the application module only. Library modules stay flavour-free
 * so their variant matrix is 2 rather than 6; anything below :app reads
 * configuration through DI instead of BuildConfig.
 *
 * Secrets never appear here — see PHASE_0_1_PLAN §C and finding F7.
 */
enum class AppEnvironment(
    val flavorName: String,
    val idSuffix: String?,
    val apiBaseUrl: String,
    val wsBaseUrl: String,
    /**
     * OTLP/HTTP collector. Blank disables telemetry export entirely.
     *
     * Blank for prod on purpose: pointing a mobile fleet at a collector is a
     * capacity and cost decision (audit G1), not something a build default
     * should make.
     */
    val otlpEndpoint: String = "",
) {
    /**
     * Local development against the docker stack.
     *
     * 127.0.0.1, NOT 10.0.2.2. The loopback works on a **physical device**
     * via `adb reverse`, which is the common case here; 10.0.2.2 is the host
     * alias visible only from an emulator and resolves to nothing on real
     * hardware. The Flutter reference reached the same conclusion
     * ([environment.dart:105]) and defaults the same way.
     *
     * Before running a dev build on a device:
     *   adb reverse tcp:8080 tcp:8080
     *   adb reverse tcp:8093 tcp:8093
     *
     * For an emulator, forward nothing and point these at 10.0.2.2 instead.
     */
    DEV(
        flavorName = "dev",
        idSuffix = ".dev",
        apiBaseUrl = "http://10.0.2.2:8080",
        wsBaseUrl = "ws://10.0.2.2:8093/v1/ws/connect",
        // Jaeger's OTLP/HTTP receiver:
        otlpEndpoint = "http://10.0.2.2:4318",
    ),


    // TODO(B5): no staging environment has been provisioned yet. The empty
    // values are intentional — a staging build must fail loudly at the first
    // request rather than silently talk to production.
    STAGING(
        flavorName = "staging",
        idSuffix = ".staging",
        apiBaseUrl = "",
        wsBaseUrl = "",
    ),

    PROD(
        flavorName = "prod",
        idSuffix = null,
        apiBaseUrl = "https://cleestudio.com",
        wsBaseUrl = "wss://cleestudio.com/v1/ws/connect",
    ),
}

internal fun ApplicationExtension.configureFlavors() {
    buildFeatures.buildConfig = true
    flavorDimensions += "environment"
    productFlavors {
        AppEnvironment.values().forEach { env ->
            create(env.flavorName) {
                dimension = "environment"
                env.idSuffix?.let {
                    applicationIdSuffix = it
                    versionNameSuffix = it
                }
                buildConfigField("String", "API_BASE_URL", "\"${env.apiBaseUrl}\"")
                buildConfigField("String", "WS_BASE_URL", "\"${env.wsBaseUrl}\"")
                buildConfigField("String", "ENVIRONMENT", "\"${env.flavorName}\"")
                buildConfigField("String", "OTLP_ENDPOINT", "\"${env.otlpEndpoint}\"")
            }
        }
    }
}

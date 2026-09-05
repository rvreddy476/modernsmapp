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
     * `10.0.2.2` is the emulator's alias for the host loopback, and it is what
     * these values use. On a PHYSICAL device that address resolves to nothing,
     * so a device run needs the host forwarded to the handset's own loopback
     * first:
     *
     *   adb reverse tcp:8080 tcp:8080
     *
     * and these URLs changed to `127.0.0.1`. Both the API and the socket ride
     * port 8080 through the api-gateway, so one forward covers both.
     *
     * (This KDoc previously claimed the values WERE 127.0.0.1 while they were
     * already 10.0.2.2 — the comment had drifted from the code it describes.)
     */
    DEV(
        flavorName = "dev",
        idSuffix = ".dev",
        apiBaseUrl = "http://10.0.2.2:8080",
        // ORIGIN ONLY — no path. ChatSocket appends `/v1/ws/connect`, so the
        // old value `ws://10.0.2.2:8093/v1/ws/connect` was doubled into
        // `…/v1/ws/connect/v1/ws/connect` and could never connect. Nothing
        // noticed while chat was unreachable from the app; `ApiConfig` now
        // rejects a path here at construction.
        //
        // Port 8080, not 8093: the socket goes through the api-gateway like
        // every other request, so it inherits the same auth and the same
        // reverse-proxy rules rather than talking to ws-gateway directly.
        wsBaseUrl = "ws://10.0.2.2:8080",
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
        // Origin only, same rule as DEV — the connect path is ChatSocket's.
        wsBaseUrl = "wss://cleestudio.com",
    ),
}

/**
 * The host a DEV build talks to.
 *
 * Defaults to `10.0.2.2`, the Android emulator's alias for the host machine.
 * That alias is meaningless on a PHYSICAL DEVICE — it is unroutable — so a real
 * handset needs the workstation's LAN address instead:
 *
 * ```
 * ./gradlew.bat assembleDevDebug -PdevHost=192.168.1.3
 * ```
 *
 * The dev network-security config permits cleartext to any host, so the value
 * needs no registration there. (It once listed private ranges as <domain>
 * entries, which match DNS suffixes rather than CIDR blocks — a LAN host was
 * refused and the app reported itself offline.)
 *
 * DEV ONLY. Staging and prod are unaffected and keep the strict policy.
 */
/** The emulator host alias baked into the DEV entries above. */
private const val DEFAULT_DEV_HOST = "10.0.2.2"

internal fun Project.devHost(): String =
    (findProperty("devHost") as? String)?.trim()?.takeIf { it.isNotEmpty() } ?: DEFAULT_DEV_HOST

/**
 * Full base-URL overrides for a DEV build.
 *
 * `devHost` only swaps the host, keeping `http://` and `:8080` — which is right
 * for a workstation on the LAN and wrong for a TUNNEL, where the endpoint is
 * `https://api-dev.example.com` with no port and a different scheme.
 *
 * ```
 * ./gradlew.bat assembleDevDebug \
 *   -PdevApiUrl=https://api-dev.cleestudio.com \
 *   -PdevWsUrl=wss://api-dev.cleestudio.com
 * ```
 *
 * A tunnel also means real TLS, so the cleartext exemption is not used at all.
 */
internal fun Project.devApiUrl(): String? =
    (findProperty("devApiUrl") as? String)?.trim()?.takeIf { it.isNotEmpty() }

internal fun Project.devWsUrl(): String? =
    (findProperty("devWsUrl") as? String)?.trim()?.takeIf { it.isNotEmpty() }

internal fun ApplicationExtension.configureFlavors(
    devHost: String,
    devApiUrl: String? = null,
    devWsUrl: String? = null,
) {
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
                // DEV substitutes the chosen host so a physical device can
                // reach the workstation; every other flavour is untouched.
                // A full override wins outright; otherwise just swap the host.
                val isDev = env == AppEnvironment.DEV
                val api = if (isDev && devApiUrl != null) {
                    devApiUrl
                } else {
                    env.apiBaseUrl.replace(DEFAULT_DEV_HOST, devHost)
                }
                val ws = if (isDev && devWsUrl != null) {
                    devWsUrl
                } else {
                    env.wsBaseUrl.replace(DEFAULT_DEV_HOST, devHost)
                }
                buildConfigField("String", "API_BASE_URL", "\"$api\"")
                buildConfigField("String", "WS_BASE_URL", "\"$ws\"")
                buildConfigField("String", "ENVIRONMENT", "\"${env.flavorName}\"")
                buildConfigField(
                    "String",
                    "OTLP_ENDPOINT",
                    "\"${env.otlpEndpoint.replace(DEFAULT_DEV_HOST, devHost)}\"",
                )
            }
        }
    }
}

/**
 * The Banuba Video Editor licence token as a Java string literal for
 * `buildConfigField`, or `""` when the secrets file is absent or empty.
 *
 * The file lives at the REPO root (`.secrets/banuba.token`, gitignored), three
 * levels above the application module. `providers.fileContents` makes it a
 * tracked configuration input, so the configuration cache is invalidated when
 * the token changes and stays valid otherwise. The value is escaped for a Java
 * literal and NEVER logged.
 */
internal fun Project.banubaLicenseToken(): String {
    val token = providers
        .fileContents(layout.projectDirectory.file("../../../.secrets/banuba.token"))
        .asText
        .map { it.trim() }
        .orElse("")
        .get()
    val escaped = token.replace("\\", "\\\\").replace("\"", "\\\"")
    return "\"$escaped\""
}

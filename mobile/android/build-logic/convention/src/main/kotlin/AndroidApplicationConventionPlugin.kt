import com.android.build.api.dsl.ApplicationExtension
import com.android.build.api.variant.ApplicationAndroidComponentsExtension
import com.us.android.convention.AndroidSdk
import com.us.android.convention.configureFlavors
import com.us.android.convention.devApiUrl
import com.us.android.convention.devHost
import com.us.android.convention.devWsUrl
import com.us.android.convention.configureKotlinAndroid
import com.us.android.convention.googleMapsAndroidKey
import com.us.android.convention.javaStringLiteral
import org.gradle.api.GradleException
import org.gradle.api.Plugin
import org.gradle.api.Project
import org.gradle.kotlin.dsl.configure

class AndroidApplicationConventionPlugin : Plugin<Project> {
    override fun apply(target: Project) = with(target) {
        pluginManager.apply("com.android.application")
        // Required because AGP 9's built-in Kotlin is disabled for KSP
        // compatibility (see gradle.properties: android.builtInKotlin=false).
        pluginManager.apply("org.jetbrains.kotlin.android")

        extensions.configure<ApplicationExtension> {
            configureKotlinAndroid(this)
            configureFlavors(devHost(), devApiUrl(), devWsUrl())

            // The Banuba licence is NOT set here any more (Feast A0,
            // 2026-09-13): it belongs to the one app that ships Banuba and is
            // added by `us.android.banuba`. See BanubaLicenseConventionPlugin.

            // Google Maps SDK for Android key, per application module: the
            // file is `.secrets/google-maps-android-<module name>.key` at the
            // repo root (gitignored), because each app's key is restricted to
            // that app's package names and signing certificate. Absent means
            // an empty key — the build succeeds and map surfaces show their
            // "Map unavailable" placeholder instead of a blank tile grid.
            // Never logged; it reaches only the manifest meta-data and
            // BuildConfig.
            val mapsKey = googleMapsAndroidKey()
            defaultConfig.manifestPlaceholders["MAPS_API_KEY"] = mapsKey
            defaultConfig.buildConfigField("String", "MAPS_API_KEY", javaStringLiteral(mapsKey))

            defaultConfig.targetSdk = AndroidSdk.TARGET
            // versionCode / versionName are set by EACH application module in
            // its own defaultConfig (Feast A0): Momentum, Feast Kitchen and
            // Feast Rider release on independent Play tracks. A release
            // variant without one fails the build — see onVariants below.
            defaultConfig.testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"

            buildTypes {
                getByName("debug") {
                    isMinifyEnabled = false
                }
                getByName("release") {
                    // Release is assembled unsigned in Phase 0. CI builds it
                    // purely to catch R8 and manifest breakage early; signing
                    // arrives with the Play track (blocker B6).
                    isMinifyEnabled = true
                    isShrinkResources = true
                    proguardFiles(
                        getDefaultProguardFile("proguard-android-optimize.txt"),
                        "proguard-rules.pro",
                    )
                }
            }

            packaging.resources.excludes += setOf(
                "/META-INF/{AL2.0,LGPL2.1}",
                "/META-INF/LICENSE*",
            )

            testOptions.unitTests.isIncludeAndroidResources = true
            testOptions.unitTests.isReturnDefaultValues = true
        }

        extensions.configure<ApplicationAndroidComponentsExtension> {
            // Now that the plugin no longer hard-codes a version, an app that
            // forgets its own would publish with none. Debug builds may omit
            // it; a release may not.
            //
            // Read from the DSL, NOT from `variant.outputs[].versionCode`: that
            // property is non-null even when no module set a version (verified
            // by mutation on 2026-09-13 — removing versionCode from :app left
            // it populated), so a check on it can never fire. The DSL holds
            // exactly what the module declared.
            var dslDefaultVersionCode: Int? = null
            var dslFlavorVersionCodes: Map<String, Int?> = emptyMap()
            finalizeDsl { android ->
                dslDefaultVersionCode = android.defaultConfig.versionCode
                dslFlavorVersionCodes = android.productFlavors.associate { it.name to it.versionCode }
            }
            onVariants { variant ->
                // AGP's precedence: the first flavour (by dimension order) that
                // sets one wins, otherwise defaultConfig.
                val effectiveVersionCode = variant.productFlavors
                    .firstNotNullOfOrNull { (_, flavor) -> dslFlavorVersionCodes[flavor] }
                    ?: dslDefaultVersionCode
                if (variant.buildType == "release" && effectiveVersionCode == null) {
                    throw GradleException(
                        "$path: release variant '${variant.name}' has no versionCode. " +
                            "Set versionCode and versionName in this module's own " +
                            "android.defaultConfig — the application convention plugin " +
                            "no longer supplies them.",
                    )
                }

                // The Banuba licence belongs to `us.android.banuba` alone. An
                // application that has the field WITHOUT that plugin means the
                // token leaked back into shared configuration — which would
                // put it in the Kitchen and Rider APKs.
                //
                // Checked as the variant's FIRST build step, not here: AGP
                // refuses to read `buildConfigFields` while the project is
                // still configuring ("Cannot query the value of property
                // 'buildConfigFields'…"), so an eager read would break every
                // application that does not apply the Banuba plugin. The
                // doFirst captures only a provider and strings, which keeps it
                // configuration-cache safe.
                if (!pluginManager.hasPlugin(BanubaLicenseConventionPlugin.ID)) {
                    val fields = variant.buildConfigFields
                    val variantName = variant.name
                    val projectPath = path
                    val preBuildName = "pre${variantName.replaceFirstChar { it.uppercase() }}Build"
                    tasks.configureEach {
                        if (name == preBuildName) {
                            doFirst {
                                val leaked = fields?.get()?.containsKey(
                                    BanubaLicenseConventionPlugin.BANUBA_LICENSE_FIELD,
                                ) == true
                                if (leaked) {
                                    throw GradleException(
                                        "$projectPath: variant '$variantName' declares " +
                                            "${BanubaLicenseConventionPlugin.BANUBA_LICENSE_FIELD} " +
                                            "but does not apply ${BanubaLicenseConventionPlugin.ID}. " +
                                            "The Banuba licence must only be added by that plugin.",
                                    )
                                }
                            }
                        }
                    }
                }
            }
        }
    }
}

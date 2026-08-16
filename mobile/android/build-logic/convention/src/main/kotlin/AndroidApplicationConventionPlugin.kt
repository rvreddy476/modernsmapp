import com.android.build.api.dsl.ApplicationExtension
import com.us.android.convention.AndroidSdk
import com.us.android.convention.configureFlavors
import com.us.android.convention.configureKotlinAndroid
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
            configureFlavors()

            defaultConfig.targetSdk = AndroidSdk.TARGET
            defaultConfig.versionCode = 1
            defaultConfig.versionName = "0.1.0"
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
    }
}

import com.android.build.api.dsl.LibraryExtension
import com.us.android.convention.configureKotlinAndroid
import org.gradle.api.Plugin
import org.gradle.api.Project
import org.gradle.kotlin.dsl.configure

class AndroidLibraryConventionPlugin : Plugin<Project> {
    override fun apply(target: Project) = with(target) {
        pluginManager.apply("com.android.library")
        // Required because AGP 9's built-in Kotlin is disabled for KSP
        // compatibility (see gradle.properties: android.builtInKotlin=false).
        pluginManager.apply("org.jetbrains.kotlin.android")

        extensions.configure<LibraryExtension> {
            configureKotlinAndroid(this)

            defaultConfig.testInstrumentationRunner =
                "androidx.test.runner.AndroidJUnitRunner"

            testOptions.unitTests.isIncludeAndroidResources = true
            testOptions.unitTests.isReturnDefaultValues = true

            // Library modules deliberately get no product flavors. Only :app
            // resolves the environment; everything below it receives config
            // through DI. Keeps each library's variant matrix at 2, not 6.
        }
    }
}

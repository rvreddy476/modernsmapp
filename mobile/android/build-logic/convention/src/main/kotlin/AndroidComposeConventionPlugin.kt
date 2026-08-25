import com.android.build.api.dsl.ApplicationExtension
import com.android.build.api.dsl.CommonExtension
import com.android.build.api.dsl.LibraryExtension
import com.us.android.convention.libs
import org.gradle.api.Plugin
import org.gradle.api.Project
import org.gradle.kotlin.dsl.dependencies

/**
 * Applied ON TOP OF us.android.application or us.android.library.
 * Never applied alone — it assumes an Android extension already exists.
 *
 * Since Kotlin 2.0 the Compose compiler is a Kotlin plugin, so there is no
 * separate composeCompiler version to keep in step with Kotlin.
 */
class AndroidComposeConventionPlugin : Plugin<Project> {
    override fun apply(target: Project) = with(target) {
        pluginManager.apply("org.jetbrains.kotlin.plugin.compose")

        // The Android extension is registered under its concrete type, never
        // as the base CommonExtension, so look it up by concrete type.
        val extension: CommonExtension =
            extensions.findByType(ApplicationExtension::class.java)
                ?: extensions.findByType(LibraryExtension::class.java)
                ?: error(
                    "us.android.compose must be applied on top of " +
                        "us.android.application or us.android.library",
                )
        extension.buildFeatures.compose = true

        dependencies {
            val bom = libs.findLibrary("compose-bom").get()
            add("implementation", platform(bom))
            add("androidTestImplementation", platform(bom))

            add("implementation", libs.findLibrary("compose-ui").get())
            add("implementation", libs.findLibrary("compose-ui-graphics").get())
            add("implementation", libs.findLibrary("compose-ui-tooling-preview").get())
            add("implementation", libs.findLibrary("compose-material3").get())
            add("implementation", libs.findLibrary("compose-material-icons-core").get())
            add("implementation", libs.findLibrary("androidx-lifecycle-runtime-compose").get())

            // ui-tooling is debug-only: it drags the whole preview/inspector
            // machinery in and must never reach a release binary.
            add("debugImplementation", libs.findLibrary("compose-ui-tooling").get())
            add("debugImplementation", libs.findLibrary("compose-ui-test-manifest").get())

            add("androidTestImplementation", libs.findLibrary("compose-ui-test-junit4").get())
        }
    }
}

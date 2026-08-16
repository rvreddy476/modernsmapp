import com.us.android.convention.configureKotlinJvm
import org.gradle.api.Plugin
import org.gradle.api.Project

/**
 * Plain Kotlin/JVM module — no Android dependency at all.
 *
 * Used by :core:model. If a model module ever needs an Android type, that
 * is a design error to fix in the design, not by switching this plugin
 * (PHASE_0_1_PLAN §B, module dependency rules).
 */
class JvmLibraryConventionPlugin : Plugin<Project> {
    override fun apply(target: Project) = with(target) {
        pluginManager.apply("org.jetbrains.kotlin.jvm")
        configureKotlinJvm()
    }
}

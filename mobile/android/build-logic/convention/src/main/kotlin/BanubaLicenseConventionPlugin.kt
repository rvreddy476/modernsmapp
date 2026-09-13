import com.android.build.api.dsl.ApplicationExtension
import com.us.android.convention.banubaLicenseToken
import org.gradle.api.GradleException
import org.gradle.api.Plugin
import org.gradle.api.Project
import org.gradle.kotlin.dsl.configure

/**
 * `us.android.banuba` — the Banuba licence token, for the ONE application that
 * ships Banuba (Momentum, `:app`).
 *
 * It used to live in the generic application plugin, which meant every future
 * application module would have carried the token in its BuildConfig whether
 * or not it contained a single Banuba class. Feast Kitchen and Feast Rider are
 * such apps: they must ship neither the SDK nor its licence.
 *
 * Read from the repo-root secrets file (gitignored) at configuration time;
 * absent or empty means the app builds without a licence and the reel flow
 * uses the Media3 studio. The token is never printed: it goes straight into
 * BuildConfig, which AppModule alone reads.
 *
 * Apply it AFTER `us.android.application`. The application plugin fails the
 * build if any application has the field without this plugin.
 */
class BanubaLicenseConventionPlugin : Plugin<Project> {
    override fun apply(target: Project) = with(target) {
        if (!pluginManager.hasPlugin("com.android.application")) {
            throw GradleException(
                "$path: us.android.banuba must be applied after us.android.application — " +
                    "the licence is an application BuildConfig field.",
            )
        }
        extensions.configure<ApplicationExtension> {
            defaultConfig.buildConfigField("String", BANUBA_LICENSE_FIELD, banubaLicenseToken())
        }
    }

    companion object {
        const val ID = "us.android.banuba"
        const val BANUBA_LICENSE_FIELD = "BANUBA_LICENSE_TOKEN"
    }
}

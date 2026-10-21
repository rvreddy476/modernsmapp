import com.android.build.api.dsl.ApplicationExtension
import com.us.android.convention.googleOAuthAndroidClientId
import com.us.android.convention.googleOAuthRedirectScheme
import com.us.android.convention.javaStringLiteral
import org.gradle.api.GradleException
import org.gradle.api.Plugin
import org.gradle.api.Project
import org.gradle.kotlin.dsl.configure

/**
 * `us.android.google-signin` — the Google OAuth client for an application
 * that offers "Continue with Google" (Momentum, `:app`; web parity, founder
 * 21 Oct 2026).
 *
 * Reads `.secrets/google-oauth-android-<module name>.client-id` at the repo
 * root (gitignored), the same way `us.android.banuba` reads its token, and
 * sets two things from the one value:
 *
 *  - `BuildConfig.GOOGLE_OAUTH_ANDROID_CLIENT_ID`, which AppModule hands to
 *    `:core:auth` as `GoogleOAuthConfig`;
 *  - the `GOOGLE_OAUTH_REDIRECT_SCHEME` manifest placeholder — the client id
 *    reversed (`com.googleusercontent.apps.<id>`), the custom scheme Google
 *    requires an installed-app client to redirect on.
 *
 * Absent or empty file: a blank id, an inert placeholder scheme, and the
 * Login screen shows no Google button. The id is never printed. The founder
 * creates the Android OAuth client (package name + signing SHA-1) in Google
 * Cloud; nothing here invents one.
 *
 * Apply AFTER `us.android.application`: the field and placeholder belong to
 * an application's defaultConfig.
 */
class GoogleSignInConventionPlugin : Plugin<Project> {
    override fun apply(target: Project) = with(target) {
        if (!pluginManager.hasPlugin("com.android.application")) {
            throw GradleException(
                "$path: us.android.google-signin must be applied after us.android.application — " +
                    "the client id is an application BuildConfig field and manifest placeholder.",
            )
        }
        val clientId = googleOAuthAndroidClientId()
        extensions.configure<ApplicationExtension> {
            defaultConfig.buildConfigField("String", CLIENT_ID_FIELD, javaStringLiteral(clientId))
            defaultConfig.manifestPlaceholders[REDIRECT_SCHEME_PLACEHOLDER] = googleOAuthRedirectScheme(clientId)
        }
    }

    companion object {
        const val ID = "us.android.google-signin"
        const val CLIENT_ID_FIELD = "GOOGLE_OAUTH_ANDROID_CLIENT_ID"
        const val REDIRECT_SCHEME_PLACEHOLDER = "GOOGLE_OAUTH_REDIRECT_SCHEME"
    }
}

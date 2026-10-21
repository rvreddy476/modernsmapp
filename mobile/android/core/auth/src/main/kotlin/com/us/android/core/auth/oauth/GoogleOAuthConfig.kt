package com.us.android.core.auth.oauth

/**
 * What an application module knows about its Google OAuth client.
 *
 * Provided by `:app` from `BuildConfig.GOOGLE_OAUTH_ANDROID_CLIENT_ID`, which
 * the `us.android.google-signin` convention plugin fills from the gitignored
 * `.secrets/google-oauth-android-app.client-id`. Absent file, blank id: the
 * Google button stays hidden and no browser is ever opened. Apps that do not
 * apply the plugin bind nothing, and [GoogleAuthorizationFlow] sees an empty
 * `Optional` — the same outcome.
 *
 * The id is never logged. It is not a secret in the OAuth sense (it travels in
 * the authorization URL), but it is paired in Google Cloud with this app's
 * package name and signing certificate, and the founder owns that pairing.
 */
data class GoogleOAuthConfig(val clientId: String) {

    val isConfigured: Boolean get() = clientId.isNotBlank()

    /**
     * The custom scheme the browser redirects back on. Google's rule for
     * installed-app (Android) clients: the client id reversed, so
     * `1234-abc.apps.googleusercontent.com` becomes
     * `com.googleusercontent.apps.1234-abc`. The manifest declares the same
     * scheme through the `GOOGLE_OAUTH_REDIRECT_SCHEME` placeholder, computed
     * by the convention plugin with the identical rule.
     */
    val redirectScheme: String get() = redirectSchemeFor(clientId)

    val redirectUri: String get() = "$redirectScheme:$REDIRECT_PATH"

    companion object {
        const val REDIRECT_PATH = "/oauth2redirect"
        private const val CLIENT_ID_SUFFIX = ".apps.googleusercontent.com"
        private const val SCHEME_PREFIX = "com.googleusercontent.apps."

        fun redirectSchemeFor(clientId: String): String {
            val id = clientId.trim()
            return if (id.endsWith(CLIENT_ID_SUFFIX)) {
                SCHEME_PREFIX + id.removeSuffix(CLIENT_ID_SUFFIX)
            } else {
                id.split('.').reversed().joinToString(".")
            }
        }
    }
}

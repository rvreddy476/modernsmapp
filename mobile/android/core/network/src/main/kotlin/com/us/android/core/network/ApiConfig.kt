package com.us.android.core.network

/**
 * Runtime configuration for the network layer.
 *
 * Provided by `:app`, because only the application module has the flavored
 * `BuildConfig`. Library modules never read `BuildConfig` directly — that is
 * what kept `:core:*` at two variants instead of six.
 *
 * No secret ever travels through here. An APK's constants are trivially
 * readable, which is the whole of finding F7.
 */
data class ApiConfig(
    val baseUrl: String,
    val wsBaseUrl: String,
    val clientVersion: String,
    val environment: String,
    val isDebug: Boolean,
) {
    init {
        // Fails fast rather than letting Retrofit throw a vaguer error later.
        // Catches the staging flavor (blocker B5), whose URLs are deliberately
        // empty until infra provisions the environment.
        require(baseUrl.isNotBlank()) {
            "API base URL is empty for environment '$environment'. " +
                "The staging flavor has no URL provisioned yet (blocker B5)."
        }
    }
}

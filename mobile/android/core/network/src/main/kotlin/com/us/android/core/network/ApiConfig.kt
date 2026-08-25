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
    /**
     * The WebSocket ORIGIN — scheme, host and port, and nothing else.
     *
     * No path. `ChatSocket` appends `/v1/ws/connect` itself, so a value that
     * already carried the path produced
     * `ws://host:8093/v1/ws/connect/v1/ws/connect`, which the gateway answers
     * with 404 and OkHttp reports as a plain connection failure. The socket
     * then looks like a network problem rather than a configuration one, and
     * the reconnect loop retries the same wrong URL forever.
     *
     * The DEV flavor shipped exactly that value. Nothing caught it because no
     * screen opened the socket until chat was wired into navigation, so the
     * check below is what makes a future one impossible.
     */
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
        require(wsBaseUrl.isBlank() || !wsBaseUrl.substringAfter("://", "").contains('/')) {
            "WebSocket base URL '$wsBaseUrl' for environment '$environment' carries a path. " +
                "It must be an origin only — ChatSocket appends the connect path, and a " +
                "path here is doubled into a URL that can never connect."
        }
    }
}

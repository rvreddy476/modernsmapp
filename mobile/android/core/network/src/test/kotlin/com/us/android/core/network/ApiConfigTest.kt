package com.us.android.core.network

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * The WebSocket base URL must be an origin, not a URL with a path.
 *
 * The DEV flavor shipped `ws://10.0.2.2:8093/v1/ws/connect` while `ChatSocket`
 * appends `/v1/ws/connect` itself, so the socket dialled
 * `…/v1/ws/connect/v1/ws/connect`. That is a 404 at the gateway, which OkHttp
 * surfaces as a plain connection failure — so the screen shows a socket that
 * never connects and a reconnect loop that retries the same wrong URL forever,
 * and it reads as a network problem rather than a typo in a build constant.
 *
 * Nothing caught it because no screen opened the socket until chat was wired
 * into navigation. The constructor check is what makes a repeat impossible;
 * these tests are what keep the check.
 */
class ApiConfigTest {

    private fun config(wsBaseUrl: String) = ApiConfig(
        baseUrl = "http://10.0.2.2:8080",
        wsBaseUrl = wsBaseUrl,
        clientVersion = "1.0",
        environment = "dev",
        isDebug = true,
    )

    @Test
    fun `an origin is accepted`() {
        assertThat(config("ws://10.0.2.2:8080").wsBaseUrl).isEqualTo("ws://10.0.2.2:8080")
        assertThat(config("wss://cleestudio.com").wsBaseUrl).isEqualTo("wss://cleestudio.com")
    }

    @Test
    fun `a url carrying the connect path is refused`() {
        val error = runCatching { config("ws://10.0.2.2:8093/v1/ws/connect") }.exceptionOrNull()

        assertThat(error).isInstanceOf(IllegalArgumentException::class.java)
        assertThat(error).hasMessageThat().contains("carries a path")
    }

    @Test
    fun `any path is refused, not just the connect one`() {
        assertThat(runCatching { config("wss://example.com/socket") }.isFailure).isTrue()
    }

    /**
     * Staging has no URLs provisioned (blocker B5). An empty value must not
     * trip the path check — the blank-URL failure belongs to `baseUrl`, and
     * two errors for one cause is worse than one.
     */
    @Test
    fun `an empty websocket url is left to the staging blocker`() {
        assertThat(config("").wsBaseUrl).isEmpty()
    }
}

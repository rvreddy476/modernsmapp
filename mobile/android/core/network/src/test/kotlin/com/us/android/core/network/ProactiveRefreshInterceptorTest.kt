package com.us.android.core.network

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.interceptor.AuthInterceptor
import com.us.android.core.network.interceptor.ProactiveRefreshInterceptor
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import org.junit.After
import org.junit.Before
import org.junit.Test
import java.util.concurrent.atomic.AtomicInteger

/**
 * H2, cold start: after process death the access token (memory only) is gone.
 * The first authenticated request mints one BEFORE it goes out, rather than
 * every request of the cold start 401-ing and parking in the authenticator.
 */
class ProactiveRefreshInterceptorTest {

    private lateinit var server: MockWebServer
    private lateinit var config: ApiConfig
    private val refreshes = AtomicInteger(0)

    @Volatile
    private var token: String? = null

    @Volatile
    private var sessionExists = true

    private val provider = object : TokenProvider {
        override fun currentAccessToken(): String? = token
    }

    private val refresher = object : TokenRefresher {
        override suspend fun refresh(): String? = error("the interceptor asks only when stale")

        override suspend fun refreshIfStale(failedToken: String?): String? {
            refreshes.incrementAndGet()
            token = "minted-${refreshes.get()}"
            return token
        }

        override fun shouldRefreshBeforeRequest(): Boolean = sessionExists && token == null
    }

    private lateinit var client: OkHttpClient

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        config = ApiConfig(
            baseUrl = server.url("/").toString(),
            wsBaseUrl = "ws://localhost",
            clientVersion = "test",
            environment = "test",
            isDebug = true,
        )
        client = OkHttpClient.Builder()
            .addInterceptor(ProactiveRefreshInterceptor(provider, refresher, config))
            .addInterceptor(AuthInterceptor(provider, config))
            .build()
    }

    @After
    fun tearDown() = server.close()

    private fun call(path: String, post: Boolean = false): Int {
        server.enqueue(MockResponse.Builder().code(200).body("{}").build())
        val builder = Request.Builder().url(server.url(path))
        if (post) builder.post("{}".toRequestBody())
        return client.newCall(builder.build()).execute().use { it.code }
    }

    @Test
    fun `the first request of a cold start goes out already carrying a token`() {
        call("/v1/feed/home")

        assertThat(refreshes.get()).isEqualTo(1)
        assertThat(server.takeRequest().headers["Authorization"]).isEqualTo("Bearer minted-1")

        // Minted once: the next request just uses it.
        call("/v1/feed/home")
        assertThat(refreshes.get()).isEqualTo(1)
        assertThat(server.takeRequest().headers["Authorization"]).isEqualTo("Bearer minted-1")
    }

    @Test
    fun `token-minting routes never trigger a refresh first`() {
        call("/v1/auth/login", post = true)
        call("/v1/auth/refresh", post = true)
        call("/v1/auth/register", post = true)

        assertThat(refreshes.get()).isEqualTo(0)
    }

    @Test
    fun `no session means no refresh`() {
        sessionExists = false

        call("/v1/feed/home")

        assertThat(refreshes.get()).isEqualTo(0)
        assertThat(server.takeRequest().headers["Authorization"]).isNull()
    }
}

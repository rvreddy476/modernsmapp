package com.us.android.core.media.sound

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiConfig
import com.us.android.core.network.TokenProvider
import com.us.android.core.network.interceptor.AuthInterceptor
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.OkHttpClient
import okhttp3.Request
import org.junit.After
import org.junit.Before
import org.junit.Test

/**
 * Where a sound's bytes are asked for, and who is shown the viewer's token
 * on the way.
 *
 * `GET v1/audio/{id}/serve` is OUR route and needs the bearer; it answers 307
 * to a signed storage link on another host. The sound player reads through
 * the same OkHttp client as every other media request, so what that client
 * does on the redirect is what decides whether a credential that
 * authenticates as the user reaches the object store. It must not: the
 * store rejects a request with two kinds of authentication, and — the larger
 * half — the token is not the store's to see.
 */
class SoundServeUrlTest {

    private lateinit var api: MockWebServer
    private lateinit var storage: MockWebServer

    @Before
    fun setUp() {
        api = MockWebServer().apply { start() }
        storage = MockWebServer().apply { start() }
    }

    @After
    fun tearDown() {
        api.close()
        storage.close()
    }

    // ── The address ─────────────────────────────────────────────────────

    @Test
    fun `the sound's bytes come from the serve route under the API base`() {
        assertThat(soundServeUrl("http://127.0.0.1:8080", "s-1")).isEqualTo("http://127.0.0.1:8080/v1/audio/s-1/serve")
        assertThat(soundServeUrl("https://api.example.test/", "s-1"))
            .isEqualTo("https://api.example.test/v1/audio/s-1/serve")
        assertThat(soundServeUrl("https://api.example.test/gateway", "s-1"))
            .isEqualTo("https://api.example.test/gateway/v1/audio/s-1/serve")
        assertThat(soundServeUrl("https://api.example.test/gateway/", "s-1"))
            .isEqualTo("https://api.example.test/gateway/v1/audio/s-1/serve")
    }

    @Test
    fun `an id cannot break out of its place in the address`() {
        assertThat(soundServeUrl("https://api.example.test", "a/b?c"))
            .isEqualTo("https://api.example.test/v1/audio/a%2Fb%3Fc/serve")
        assertThat(soundServeUrl("https://api.example.test", "../../auth/login"))
            .isEqualTo("https://api.example.test/v1/audio/..%2F..%2Fauth%2Flogin/serve")
        assertThat(soundServeUrl("https://api.example.test", "s1#frag"))
            .isEqualTo("https://api.example.test/v1/audio/s1%23frag/serve")
    }

    @Test
    fun `no id or no base is no address`() {
        assertThat(soundServeUrl("https://api.example.test", "")).isNull()
        assertThat(soundServeUrl("https://api.example.test", "   ")).isNull()
        assertThat(soundServeUrl("not a url", "s1")).isNull()
        assertThat(soundServeUrl("", "s1")).isNull()
    }

    // ── The redirect ────────────────────────────────────────────────────

    /** The media chain's client, as far as credentials go: the app's interceptor on OkHttp's defaults. */
    private fun client() = OkHttpClient.Builder()
        .addInterceptor(
            AuthInterceptor(
                tokenProvider = object : TokenProvider {
                    override fun currentAccessToken(): String? = TOKEN
                },
                config = ApiConfig(
                    baseUrl = api.url("/").toString(),
                    wsBaseUrl = "ws://example.invalid",
                    clientVersion = "test",
                    environment = "test",
                    isDebug = true,
                ),
            ),
        )
        .build()

    @Test
    fun `the serve route is asked with the token and the storage link it redirects to is not`() {
        val signed = storage.url("/audio/s1.m4a?X-Amz-Signature=abc&X-Amz-Expires=300")
        api.enqueue(MockResponse.Builder().code(307).addHeader("Location", signed.toString()).build())
        storage.enqueue(MockResponse.Builder().code(200).body("bytes").build())
        val serve = checkNotNull(soundServeUrl(api.url("/").toString(), "s1"))

        val body = client().newCall(Request.Builder().url(serve).build()).execute().use { it.body.string() }

        assertThat(body).isEqualTo("bytes")
        val asked = api.takeRequest()
        assertThat(asked.target).isEqualTo("/v1/audio/s1/serve")
        assertThat(asked.headers["Authorization"]).isEqualTo("Bearer $TOKEN")
        val fetched = storage.takeRequest()
        assertThat(fetched.target).isEqualTo("/audio/s1.m4a?X-Amz-Signature=abc&X-Amz-Expires=300")
        assertThat(fetched.headers["Authorization"]).isNull()
    }

    /** A player asks for a range; the redirect must keep it, and still drop the token. */
    @Test
    fun `a ranged request keeps its range across the redirect and still sheds the token`() {
        api.enqueue(
            MockResponse.Builder().code(307).addHeader("Location", storage.url("/audio/s1.m4a").toString()).build(),
        )
        storage.enqueue(MockResponse.Builder().code(206).body("es").build())
        val serve = checkNotNull(soundServeUrl(api.url("/").toString(), "s1"))

        client().newCall(Request.Builder().url(serve).header("Range", "bytes=3-").build()).execute().close()

        assertThat(api.takeRequest().headers["Authorization"]).isEqualTo("Bearer $TOKEN")
        val fetched = storage.takeRequest()
        assertThat(fetched.headers["Range"]).isEqualTo("bytes=3-")
        assertThat(fetched.headers["Authorization"]).isNull()
    }

    /** A sound whose source went private: the refusal is the player's error, and nothing is followed. */
    @Test
    fun `a refused sound is a 404 and no storage request is made`() {
        api.enqueue(
            MockResponse.Builder().code(404)
                .body("""{"error":{"code":"NOT_FOUND","message":"Audio track not found"}}""").build(),
        )
        val serve = checkNotNull(soundServeUrl(api.url("/").toString(), "s1"))

        val code = client().newCall(Request.Builder().url(serve).build()).execute().use { it.code }

        assertThat(code).isEqualTo(404)
        assertThat(storage.requestCount).isEqualTo(0)
    }

    private companion object {
        const val TOKEN = "test-access-token"
    }
}

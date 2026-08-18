package com.us.android.core.network

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.interceptor.AuthInterceptor
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.OkHttpClient
import okhttp3.Request
import org.junit.After
import org.junit.Before
import org.junit.Test

/**
 * The access token must reach our gateway and NOTHING else.
 *
 * This is a credential-scoping test, not a convenience one. It was written
 * after a device run on 2026-08-18 where reels rendered a black frame: Media3
 * shares this client, media segment URLs are absolute pre-signed object-store
 * links, and the bearer was being attached to them. MinIO rejected every
 * segment with 400 "request has multiple authentication types, please use
 * one".
 *
 * Broken playback was the visible symptom. The real defect is that the app was
 * handing a credential that authenticates as the user to a host that is not
 * ours — in production those URLs point at CloudFront.
 */
class AuthOriginTest {

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

    private fun clientFor(apiBaseUrl: String) = OkHttpClient.Builder()
        .addInterceptor(
            AuthInterceptor(
                tokenProvider = object : TokenProvider {
                    override fun currentAccessToken(): String? = TOKEN
                },
                config = ApiConfig(
                    baseUrl = apiBaseUrl,
                    wsBaseUrl = "ws://example.invalid",
                    clientVersion = "test",
                    environment = "test",
                    isDebug = true,
                ),
            ),
        )
        .build()

    private fun get(client: OkHttpClient, url: String) {
        client.newCall(Request.Builder().url(url).build()).execute().close()
    }

    @Test
    fun `the api origin receives the bearer token`() {
        api.enqueue(MockResponse.Builder().code(200).body("{}").build())

        get(clientFor(api.url("/").toString()), api.url("/v1/feed/home").toString())

        assertThat(api.takeRequest().headers["Authorization"]).isEqualTo("Bearer $TOKEN")
    }

    /** The case that broke reels on hardware. */
    @Test
    fun `a foreign origin receives no bearer token`() {
        storage.enqueue(MockResponse.Builder().code(200).body("segment").build())

        get(clientFor(api.url("/").toString()), storage.url("/media/a/hls/360p_000.ts").toString())

        assertThat(storage.takeRequest().headers["Authorization"]).isNull()
    }

    /**
     * Host alone is not a sufficient check. In local development the gateway is
     * 127.0.0.1:8080 and the object store is 127.0.0.1:9000 — same host,
     * different service — so a host-only comparison would still leak the token
     * to storage.
     */
    @Test
    fun `same host on a different port is still foreign`() {
        storage.enqueue(MockResponse.Builder().code(200).body("segment").build())

        // Both MockWebServers bind 127.0.0.1; only the ports differ.
        get(clientFor(api.url("/").toString()), storage.url("/media/seg.ts").toString())

        val sent = storage.takeRequest()
        assertThat(sent.headers["Authorization"]).isNull()
    }

    /** Auth endpoints stay exempt — a stale token on /login is noise at best. */
    @Test
    fun `login is still exempt on the api origin`() {
        api.enqueue(MockResponse.Builder().code(200).body("{}").build())

        get(clientFor(api.url("/").toString()), api.url("/v1/auth/login").toString())

        assertThat(api.takeRequest().headers["Authorization"]).isNull()
    }

    private companion object {
        const val TOKEN = "test-access-token"
    }
}

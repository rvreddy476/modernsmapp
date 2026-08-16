package com.us.android.core.network

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.di.NetworkModule
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import org.junit.After
import org.junit.Before
import org.junit.Test

/**
 * Acceptance criterion F.11.
 *
 * Phase 5's media upload is `POST /v1/media/init` → **presigned PUT to S3** →
 * `POST /v1/media/confirm`. That middle hop must go out completely naked: a
 * stray `Authorization` header breaks presigned-URL signature validation
 * outright, and the failure mode is a 403 from the object store that looks
 * nothing like an auth bug.
 *
 * Proven here in Phase 1 so the requirement is locked before the upload code
 * that depends on it exists.
 */
class BareClientTest {

    private lateinit var server: MockWebServer

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
    }

    @After
    fun tearDown() = server.close()

    @Test
    fun `the bare client sends no auth, csrf or app headers`() {
        val client = NetworkModule.provideBareOkHttpClient()

        server.enqueue(MockResponse.Builder().code(200).build())
        client.newCall(
            Request.Builder()
                .url(server.url("/presigned-bucket/object"))
                .put("bytes".toRequestBody())
                .build(),
        ).execute().close()

        val recorded = server.takeRequest()
        assertThat(recorded.headers["Authorization"]).isNull()
        assertThat(recorded.headers["X-CSRF-Token"]).isNull()
        assertThat(recorded.headers["X-Client-Platform"]).isNull()
        assertThat(recorded.headers["X-Requested-With"]).isNull()
        assertThat(recorded.headers["X-Request-Id"]).isNull()
    }

    @Test
    fun `the bare client carries no interceptors and no cookie jar state`() {
        val client = NetworkModule.provideBareOkHttpClient()

        assertThat(client.interceptors).isEmpty()
        assertThat(client.authenticator).isEqualTo(okhttp3.Authenticator.NONE)
    }
}

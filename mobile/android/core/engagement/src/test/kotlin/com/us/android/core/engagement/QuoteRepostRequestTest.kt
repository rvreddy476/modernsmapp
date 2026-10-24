package com.us.android.core.engagement

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.engagement.data.EngagementApi
import com.us.android.core.engagement.data.EngagementRepository
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory

/**
 * A quote repost (2026-10-24) against post-service CreateRepost
 * (internal/http/handler.go:3410-3470): `{type: plain|quote, quote_text}`,
 * 409 ALREADY_REPOSTED, 422 QUOTE_TEXT_REQUIRED / QUOTE_TEXT_TOO_LONG.
 */
class QuoteRepostRequestTest {

    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
    }
    private lateinit var server: MockWebServer
    private lateinit var repository: EngagementRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder().baseUrl(server.url("/")).client(OkHttpClient())
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType())).build()
            .create(EngagementApi::class.java)
        repository = EngagementRepository(api, ErrorMapper(json))
    }

    @After
    fun tearDown() = server.close()

    private fun answer(code: Int, body: String) = server.enqueue(
        MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build(),
    )

    @Test
    fun `a quote posts type quote with the trimmed words`() = runBlocking {
        answer(201, """{"data":{"id":"rp1","type":"quote","quote_text":"so true"}}""")

        val result = repository.quote("p1", "  so true  ")

        assertThat(result).isInstanceOf(AppResult.Success::class.java)
        val sent = server.takeRequest()
        assertThat("${sent.method} ${sent.url.encodedPath}").isEqualTo("POST /v1/posts/p1/repost")
        assertThat(sent.body!!.utf8()).isEqualTo("""{"type":"quote","quote_text":"so true"}""")
    }

    @Test
    fun `a plain repost still sends no quote_text at all`() = runBlocking {
        answer(201, """{"data":{"id":"rp1","type":"plain"}}""")

        repository.repost("p1")

        assertThat(server.takeRequest().body!!.utf8()).isEqualTo("""{"type":"plain"}""")
    }

    @Test
    fun `an already reposted post is refused with its code`() = runBlocking {
        answer(409, """{"error":{"code":"ALREADY_REPOSTED","message":"You already reposted this post"}}""")

        val error = (repository.quote("p1", "x") as AppResult.Failure).error

        assertThat((error as AppError.Unknown).code).isEqualTo("ALREADY_REPOSTED")
    }
}

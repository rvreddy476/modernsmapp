package com.us.android.core.engagement

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.result.AppResult
import com.us.android.core.engagement.data.RepostStateApi
import com.us.android.core.engagement.data.RepostStateRepository
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory

/** The share sheet's Repost row reads `GET /v1/posts/:id/repost/me` (post-service `GetRepostState`). */
class RepostStateTest {

    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
    }
    private lateinit var server: MockWebServer
    private lateinit var repository: RepostStateRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(RepostStateApi::class.java)
        repository = RepostStateRepository(api, ErrorMapper(json))
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(code: Int, body: String) {
        server.enqueue(MockResponse.Builder().code(code).body(body).build())
    }

    @Test
    fun `a repost on record reads as reposted`() = runTest {
        enqueue(
            200,
            """{"data":{"has_reposted":true,"repost_id":"r1","type":"plain","created_at":"2026-10-17T09:00:00Z"}}""",
        )

        val result = repository.hasReposted("p1")

        assertThat(server.takeRequest().target).isEqualTo("/v1/posts/p1/repost/me")
        assertThat((result as AppResult.Success).data).isTrue()
    }

    @Test
    fun `no repost reads as not reposted, and a failure is a failure`() = runTest {
        enqueue(200, """{"data":{"has_reposted":false}}""")
        enqueue(500, """{"error":{"code":"INTERNAL_ERROR","message":"x"}}""")

        assertThat((repository.hasReposted("p1") as AppResult.Success).data).isFalse()
        assertThat(repository.hasReposted("p1")).isInstanceOf(AppResult.Failure::class.java)
    }
}

package com.us.android.core.profile.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.result.AppResult
import com.us.android.core.model.ProfileCounts
import com.us.android.core.model.withPostCount
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.test.runTest
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
 * The profile header's post number (2026-10-17): post-service's per-author
 * counts, because the profile payload's `post_count` is never written.
 */
class AuthorPostCountsTest {

    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
    }
    private lateinit var server: MockWebServer
    private lateinit var repository: AuthorPostCountsRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val retrofit = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .client(OkHttpClient())
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
        repository = AuthorPostCountsRepository(retrofit.create(AuthorPostCountsApi::class.java), ErrorMapper(json))
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(code: Int, body: String) {
        server.enqueue(
            MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build(),
        )
    }

    @Test
    fun `the total is read from the per-author counts route`() = runTest {
        enqueue(200, """{"data":{"post":5,"long_video":2,"total":7}}""")

        val result = repository.total("u1")

        assertThat(server.takeRequest().target).isEqualTo("/v1/posts/by-author/u1/counts")
        assertThat((result as AppResult.Success).data).isEqualTo(7)
    }

    @Test
    fun `an author with no posts answers zero`() = runTest {
        enqueue(200, """{"data":{"total":0}}""")

        assertThat((repository.total("u1") as AppResult.Success).data).isEqualTo(0)
    }

    @Test
    fun `a failed read is a failure, not zero`() = runTest {
        enqueue(500, """{"error":{"code":"INTERNAL_ERROR","message":"x"}}""")

        assertThat(repository.total("u1")).isInstanceOf(AppResult.Failure::class.java)
    }

    @Test
    fun `an answer without total is the sum of its kinds, clamped into Int`() {
        assertThat(authorPostTotal(mapOf("post" to 5L, "flick" to 1L))).isEqualTo(6)
        assertThat(authorPostTotal(mapOf("total" to Long.MAX_VALUE))).isEqualTo(Int.MAX_VALUE)
        assertThat(authorPostTotal(emptyMap())).isEqualTo(0)
    }

    @Test
    fun `the post total replaces the post count and keeps the others`() {
        val stale = ProfileCounts(followers = 9, following = 4, friends = 2, posts = 0)

        assertThat(stale.withPostCount(7)).isEqualTo(ProfileCounts(followers = 9, following = 4, friends = 2, posts = 7))
        assertThat(stale.withPostCount(null)).isEqualTo(stale)
    }
}

// The fixtures at the bottom are response bodies copied VERBATIM from a live
// capture. Reformatting recorded evidence destroys its value as proof.
@file:Suppress("MaxLineLength", "MaximumLineLength")

package com.us.android.core.engagement

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.result.AppResult
import com.us.android.core.engagement.data.EngagementApi
import com.us.android.core.engagement.data.EngagementRepository
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
 * Contract tests against payloads captured from the live stack on 2026-08-16
 * (prompt/android-api-contracts.md §2).
 *
 * These prove the DTOs deserialize the bytes the server actually sends. When a
 * payload changes, recapture and paste the new body — never edit a fixture to
 * make a test pass.
 */
class EngagementContractTest {

    private lateinit var server: MockWebServer
    private lateinit var repository: EngagementRepository
    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
    }

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .client(OkHttpClient())
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(EngagementApi::class.java)
        repository = EngagementRepository(api, ErrorMapper(json))
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(code: Int, body: String) {
        server.enqueue(
            MockResponse.Builder()
                .code(code)
                .setHeader("Content-Type", "application/json")
                .body(body)
                .build(),
        )
    }

    @Test
    fun `adding a reaction sends the captured body`() = runTest {
        enqueue(200, """{"data":{"status":"reacted"}}""")

        val result = repository.react("p")

        assertThat(result).isInstanceOf(AppResult.Success::class.java)
        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.target).isEqualTo("/v1/posts/p/reactions")
        assertThat(request.body?.utf8()).isEqualTo("""{"reaction":"like"}""")
    }

    @Test
    fun `removing a reaction sends DELETE with no body`() = runTest {
        enqueue(200, """{"data":{"status":"unreacted"}}""")

        repository.unreact("p")

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("DELETE")
        assertThat(request.body?.size ?: 0L).isEqualTo(0L)
    }

    /**
     * Bookmark is SET and CLEAR over two methods, not a toggle over one.
     *
     * Repaired and recaptured on 2026-08-17. Pinning the method per direction
     * matters because the previous contract was a single POST toggle: if the
     * repository ever regresses to sending POST for both, saving would appear
     * to work and unsaving would silently re-save.
     */
    @Test
    fun `saving sends POST and unsaving sends DELETE`() = runTest {
        enqueue(200, """{"data":{"bookmarked":true}}""")
        assertThat(repository.setBookmarked("p", true)).isInstanceOf(AppResult.Success::class.java)
        assertThat(server.takeRequest().method).isEqualTo("POST")

        enqueue(200, """{"data":{"bookmarked":false}}""")
        assertThat(repository.setBookmarked("p", false)).isInstanceOf(AppResult.Success::class.java)
        assertThat(server.takeRequest().method).isEqualTo("DELETE")
    }

    /** Both directions are idempotent: repeating a call repeats the result. */
    @Test
    fun `repeating a save is harmless`() = runTest {
        enqueue(200, """{"data":{"bookmarked":true}}""")
        enqueue(200, """{"data":{"bookmarked":true}}""")

        assertThat(repository.setBookmarked("p", true)).isInstanceOf(AppResult.Success::class.java)
        assertThat(repository.setBookmarked("p", true)).isInstanceOf(AppResult.Success::class.java)
    }

    @Test
    fun `repost sends the required type and accepts 201`() = runTest {
        enqueue(201, REPOST)

        val result = repository.repost("p")

        assertThat(result).isInstanceOf(AppResult.Success::class.java)
        val request = server.takeRequest()
        assertThat(request.body?.utf8()).isEqualTo("""{"type":"plain"}""")
    }

    /**
     * The 204 path. Routed through `noContentApiCall` because there is no
     * envelope: `apiCall` would see neither `data` nor `error` and report a
     * malformed response, turning every successful delete into a failure.
     */
    @Test
    fun `removing a repost succeeds on 204 with an empty body`() = runTest {
        server.enqueue(MockResponse.Builder().code(204).build())

        val result = repository.removeRepost("p")

        assertThat(result).isInstanceOf(AppResult.Success::class.java)
        assertThat(server.takeRequest().method).isEqualTo("DELETE")
    }

    @Test
    fun `a failed no-content delete still maps to an error`() = runTest {
        enqueue(500, """{"error":{"code":"INTERNAL_ERROR","message":"boom"}}""")

        assertThat(repository.removeRepost("p")).isInstanceOf(AppResult.Failure::class.java)
    }

    @Test
    fun `reaction validation failure surfaces`() = runTest {
        enqueue(
            400,
            """{"error":{"code":"INVALID_REQUEST","message":"Key: 'ReactionRequest.Reaction' Error:Field validation for 'Reaction' failed on the 'required' tag"}}""",
        )

        assertThat(repository.react("p")).isInstanceOf(AppResult.Failure::class.java)
    }

    private companion object {
        const val REPOST =
            """{"data":{"id":"4810e119-c7e7-4ed6-a4cb-c02ce5923c33","original_post_id":"b7f4cf83-b2fd-4096-97f9-b50b2c1751da","type":"plain","status":"active","created_at":"2026-08-16T17:53:14Z"}}"""
    }
}

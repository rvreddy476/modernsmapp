// The fixtures at the bottom are response bodies copied VERBATIM from a live
// capture. Reformatting recorded evidence destroys its value as proof.
@file:Suppress("MaxLineLength", "MaximumLineLength")

package com.us.android.feature.post.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
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
class PostContractTest {

    private lateinit var server: MockWebServer
    private lateinit var repository: PostRepository
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
            .create(PostApi::class.java)
        repository = PostRepository(api, ErrorMapper(json))
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
    fun `captured post payload deserializes`() = runTest {
        enqueue(200, POST)

        val post = (repository.getPost("b7f4cf83") as AppResult.Success).data

        assertThat(post.id).isEqualTo("b7f4cf83-b2fd-4096-97f9-b50b2c1751da")
        assertThat(post.authorId).isEqualTo("e872a073-e1b6-4e5b-94ca-0dd31f12d93f")
        assertThat(post.text).isEqualTo("tedt")
        assertThat(post.postType).isEqualTo("text")
        assertThat(post.isRepostable).isTrue()
    }

    /** `counts` is a nested object, not flat `likes` / `comments` fields. */
    @Test
    fun `counts come from the nested object and the flat siblings`() = runTest {
        enqueue(200, POST)

        val post = (repository.getPost("p") as AppResult.Success).data

        assertThat(post.counts.likes).isEqualTo(0)
        assertThat(post.counts.comments).isEqualTo(0)
        // These two are top-level, NOT inside `counts`.
        assertThat(post.counts.reposts).isEqualTo(0)
        assertThat(post.counts.views).isEqualTo(0)
    }

    /**
     * The wire carries negatives (`no_comments`, `no_likes`); the model carries
     * positives. Inverting in one place beats inverting at every call site,
     * where a single missed negation offers a control the author disabled.
     */
    @Test
    fun `author switches are inverted exactly once`() = runTest {
        enqueue(200, POST)
        val permissive = (repository.getPost("p") as AppResult.Success).data
        assertThat(permissive.allowsComments).isTrue()
        assertThat(permissive.allowsReactions).isTrue()

        enqueue(200, POST_NO_INTERACTION)
        val restricted = (repository.getPost("p") as AppResult.Success).data
        assertThat(restricted.allowsComments).isFalse()
        assertThat(restricted.allowsReactions).isFalse()
    }

    /** `rich_text` came back null on the captured post. */
    @Test
    fun `null rich_text does not break deserialization`() = runTest {
        enqueue(200, POST)

        assertThat(repository.getPost("p")).isInstanceOf(AppResult.Success::class.java)
    }

    @Test
    fun `missing post maps to NotFound`() = runTest {
        enqueue(404, """{"error":{"code":"NOT_FOUND","message":"Post not found"}}""")

        val result = repository.getPost("00000000-0000-0000-0000-000000000000")

        assertThat((result as AppResult.Failure).error).isInstanceOf(AppError.NotFound::class.java)
    }

    @Test
    fun `adding a reaction sends the captured body`() = runTest {
        enqueue(200, """{"data":{"status":"reacted"}}""")

        val result = repository.addReaction("p")

        assertThat(result).isInstanceOf(AppResult.Success::class.java)
        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.target).isEqualTo("/v1/posts/p/reactions")
        assertThat(request.body?.utf8()).isEqualTo("""{"reaction":"like"}""")
    }

    @Test
    fun `removing a reaction sends DELETE with no body`() = runTest {
        enqueue(200, """{"data":{"status":"unreacted"}}""")

        repository.removeReaction("p")

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("DELETE")
        assertThat(request.body?.size ?: 0L).isEqualTo(0L)
    }

    /**
     * The bookmark contract in one test: the endpoint is a toggle, and the
     * RESPONSE says where the post ended up. The caller must adopt it rather
     * than assume its tap flipped the state.
     */
    @Test
    fun `bookmark returns the resulting state, not the requested one`() = runTest {
        enqueue(200, """{"data":{"bookmarked":true}}""")
        assertThat((repository.toggleBookmark("p") as AppResult.Success).data).isTrue()

        enqueue(200, """{"data":{"bookmarked":false}}""")
        assertThat((repository.toggleBookmark("p") as AppResult.Success).data).isFalse()
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

        assertThat(repository.addReaction("p")).isInstanceOf(AppResult.Failure::class.java)
    }

    @Test
    fun `unknown response fields are ignored`() = runTest {
        enqueue(200, """{"data":{"id":"p","text":"hi","a_new_field":{"nested":true}}}""")

        val post = (repository.getPost("p") as AppResult.Success).data

        assertThat(post.text).isEqualTo("hi")
    }

    private companion object {
        const val POST =
            """{"data":{"id":"b7f4cf83-b2fd-4096-97f9-b50b2c1751da","author_id":"e872a073-e1b6-4e5b-94ca-0dd31f12d93f","text":"tedt","visibility":"public","content_type":"post","is_pinned":false,"rich_text":null,"no_comments":false,"no_likes":false,"post_type":"text","app_origin":"postbook","share_to_postbook":true,"review_status":"approved","language":"en","paid_promotion":false,"altered_content":false,"is_made_for_kids":false,"license":"standard","allow_embedding":true,"publish_to_feed":true,"remix_setting":"allow","comment_moderation":"none","comment_access":"everyone","original_audio_volume":1,"overlay_audio_volume":1,"distribution_rev":0,"created_at":"2026-08-15T17:05:33.673517Z","updated_at":"2026-08-15T17:05:33.673517Z","counts":{"likes":0,"comments":0},"view_count":0,"is_bookmarked":false,"repost_count":0,"is_repostable":true}}"""

        const val POST_NO_INTERACTION =
            """{"data":{"id":"p","author_id":"a","text":"quiet","no_comments":true,"no_likes":true,"post_type":"text","counts":{"likes":0,"comments":0}}}"""

        const val REPOST =
            """{"data":{"id":"4810e119-c7e7-4ed6-a4cb-c02ce5923c33","original_post_id":"b7f4cf83-b2fd-4096-97f9-b50b2c1751da","type":"plain","status":"active","created_at":"2026-08-16T17:53:14Z"}}"""
    }
}

// The fixture at the bottom is a response body copied VERBATIM from the
// 2026-08-17 live recapture. Reformatting recorded evidence destroys its value
// as proof, so this file opts out of the line-length rules rather than wrap it.
@file:Suppress("MaxLineLength", "MaximumLineLength")

package com.us.android.core.engagement

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
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
 * Contract tests for `GET /v1/posts/:postId/comments`, against the body
 * captured on 2026-08-17 (prompt/android-api-contracts.md §2).
 *
 * These exist to prove the DTO deserializes the bytes the server actually
 * sends. When the payload changes, recapture and paste the new body — never
 * edit a fixture to make a test pass.
 *
 * The harness deliberately mirrors PostContractTest rather than sharing a base
 * class: a contract test that inherits its transport from somewhere else stops
 * being readable as a statement about the wire.
 */
class CommentsContractTest {

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
    fun `captured comment payload deserializes`() = runTest {
        enqueue(200, COMMENTS)

        val comments = (repository.comments("e6106fb3") as AppResult.Success).data.items

        assertThat(comments).hasSize(1)
        val comment = comments.single()
        assertThat(comment.id).isEqualTo("46ab1085-55ca-4fc2-b649-f05527a68b97")
        assertThat(comment.authorId).isEqualTo("719e2958-f412-44ca-b94a-b00060a7fccb")
        assertThat(comment.body)
            .isEqualTo("This comment proves the native create and list contract.")
        assertThat(comment.createdAt).isEqualTo("2026-08-16T19:44:51.534734Z")
    }

    /** The text field is `body` on a comment, not `text` as it is on a post. */
    @Test
    fun `the comment text arrives under body, not text`() = runTest {
        enqueue(200, """{"data":[{"id":"c","body":"hello","text":"wrong field"}]}""")

        val comment = (repository.comments("p") as AppResult.Success).data.items.single()

        assertThat(comment.body).isEqualTo("hello")
    }

    @Test
    fun `counts and the reply flag come across`() = runTest {
        enqueue(200, COMMENT_ENGAGED)

        val comment = (repository.comments("p") as AppResult.Success).data.items.single()

        assertThat(comment.likeCount).isEqualTo(9)
        assertThat(comment.replyCount).isEqualTo(2)
        assertThat(comment.isReply).isTrue()
    }

    /**
     * The request the client actually makes.
     *
     * `limit` is asserted because it is the ONLY query parameter the capture
     * ever carried. A test that ignored the query string would let a cursor or
     * an offset parameter be added silently, and neither has been observed.
     */
    @Test
    fun `the request sends a limit and nothing else`() = runTest {
        enqueue(200, COMMENTS)

        repository.comments("e6106fb3", limit = 2)

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("GET")
        assertThat(request.target).isEqualTo("/v1/posts/e6106fb3/comments?limit=2")
    }

    /**
     * No Authorization header is added here.
     *
     * The capture succeeded anonymously, and this test pins that: the shared
     * stack attaches a token when one exists, but the comments list must not
     * become a signed-in-only surface by accident.
     */
    @Test
    fun `comments load without an Authorization header`() = runTest {
        enqueue(200, COMMENTS)

        val result = repository.comments("p")

        assertThat(result).isInstanceOf(AppResult.Success::class.java)
        assertThat(server.takeRequest().headers["Authorization"]).isNull()
    }

    /** `{"data":[]}` was the entire 2026-08-16 capture. It is a success. */
    @Test
    fun `an empty list is a success, not a failure`() = runTest {
        enqueue(200, """{"data":[]}""")

        val result = repository.comments("p")

        assertThat((result as AppResult.Success).data.items).isEmpty()
    }

    /**
     * The absence of pagination, asserted rather than assumed.
     *
     * The captured non-empty response carried no `meta`, so there is no cursor
     * to expose. This proves the repository still succeeds without one — the
     * failure mode being guarded against is a future `pagedApiCall` refactor
     * that starts requiring a cursor the server does not send.
     */
    @Test
    fun `a response with no meta still succeeds`() = runTest {
        enqueue(200, COMMENTS)

        val result = repository.comments("p")

        assertThat(result).isInstanceOf(AppResult.Success::class.java)
    }

    @Test
    fun `missing post maps to NotFound`() = runTest {
        enqueue(404, """{"error":{"code":"NOT_FOUND","message":"Post not found"}}""")

        val result = repository.comments("00000000-0000-0000-0000-000000000000")

        assertThat((result as AppResult.Failure).error).isInstanceOf(AppError.NotFound::class.java)
    }

    @Test
    fun `unknown comment fields are ignored`() = runTest {
        enqueue(
            200,
            """{"data":[{"id":"c","body":"hi","parent_comment_id":"x","pinned":true}]}""",
        )

        val comment = (repository.comments("p") as AppResult.Success).data.items.single()

        assertThat(comment.body).isEqualTo("hi")
    }

    /** Absent fields must fall back to the DTO defaults, not blow up the list. */
    @Test
    fun `a sparse comment falls back to defaults`() = runTest {
        enqueue(200, """{"data":[{"id":"c"}]}""")

        val comment = (repository.comments("p") as AppResult.Success).data.items.single()

        assertThat(comment.body).isEmpty()
        assertThat(comment.likeCount).isEqualTo(0)
        assertThat(comment.isReply).isFalse()
    }

    private companion object {
        /** Verbatim, byte for byte, from the 2026-08-17 recapture. */
        const val COMMENTS =
            """{"data":[{"id":"46ab1085-55ca-4fc2-b649-f05527a68b97","post_id":"e6106fb3-f28a-4e12-883f-2e138ea63d58","author_id":"719e2958-f412-44ca-b94a-b00060a7fccb","body":"This comment proves the native create and list contract.","like_count":0,"dislike_count":0,"reply_count":0,"is_reply":false,"created_at":"2026-08-16T19:44:51.534734Z","updated_at":"2026-08-16T19:44:51.534734Z"}]}"""

        /**
         * The captured shape with engagement values substituted. Not itself a
         * capture — the live fixture was a fresh comment with every count at
         * zero, which cannot prove the counts are read from the right keys.
         */
        const val COMMENT_ENGAGED =
            """{"data":[{"id":"46ab1085-55ca-4fc2-b649-f05527a68b97","post_id":"e6106fb3-f28a-4e12-883f-2e138ea63d58","author_id":"719e2958-f412-44ca-b94a-b00060a7fccb","body":"A reply.","like_count":9,"dislike_count":1,"reply_count":2,"is_reply":true,"created_at":"2026-08-16T19:44:51.534734Z","updated_at":"2026-08-16T19:44:51.534734Z"}]}"""
    }
}

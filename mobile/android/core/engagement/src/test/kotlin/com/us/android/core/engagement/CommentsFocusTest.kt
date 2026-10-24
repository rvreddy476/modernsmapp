package com.us.android.core.engagement

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.engagement.data.BookmarkStatusDto
import com.us.android.core.engagement.data.CommentActionsRepository
import com.us.android.core.engagement.data.CommentDto
import com.us.android.core.engagement.data.CommentRow
import com.us.android.core.engagement.data.CommentsAroundApi
import com.us.android.core.engagement.data.CommentsAroundRepository
import com.us.android.core.engagement.data.CommentsAroundSource
import com.us.android.core.engagement.data.CommentsController
import com.us.android.core.engagement.data.CreateCommentRequest
import com.us.android.core.engagement.data.EngagementApi
import com.us.android.core.engagement.data.EngagementRepository
import com.us.android.core.engagement.data.FocusTarget
import com.us.android.core.engagement.data.ReactionRequest
import com.us.android.core.engagement.data.ReactionStatusDto
import com.us.android.core.engagement.data.ReportRepository
import com.us.android.core.engagement.data.RepostDto
import com.us.android.core.engagement.data.RepostRequest
import com.us.android.core.engagement.data.ShareRequest
import com.us.android.core.engagement.data.ShareResultDto
import com.us.android.core.engagement.data.focusIndex
import com.us.android.core.engagement.data.focusTargetIn
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory

/**
 * Opening a thread ON one comment (2026-10-24): a comment or mention
 * notification's `focusComment`, read through post-service's
 * `GET /v1/posts/:postId/comments/around/:commentId` (handler.go:154,
 * ListCommentsAround :1804-1850: top-level `Comment` rows, newest first; 404
 * NOT_FOUND for a comment that is gone).
 */
class CommentsFocusTest {

    private val json = Json { ignoreUnknownKeys = true }
    private val mapper = ErrorMapper(json)
    private val actionsApi = FakeCommentActionsApi()

    /** The ordinary list: what a failed focus falls back to. */
    private class ListApi(private val rows: List<CommentDto>) : EngagementApi {
        var listed = 0
        override suspend fun getComments(postId: String, limit: Int, cursor: String?): ApiEnvelope<List<CommentDto>> {
            listed++
            return ApiEnvelope(data = rows)
        }
        override suspend fun addComment(postId: String, idempotencyKey: String, body: CreateCommentRequest): ApiEnvelope<CommentDto> =
            error("not used")
        override suspend fun addReaction(postId: String, body: ReactionRequest): ApiEnvelope<ReactionStatusDto> = error("not used")
        override suspend fun removeReaction(postId: String): ApiEnvelope<ReactionStatusDto> = error("not used")
        override suspend fun addBookmark(postId: String): ApiEnvelope<BookmarkStatusDto> = error("not used")
        override suspend fun removeBookmark(postId: String): ApiEnvelope<BookmarkStatusDto> = error("not used")
        override suspend fun repost(postId: String, body: RepostRequest): ApiEnvelope<RepostDto> = error("not used")
        override suspend fun removeRepost(postId: String) = error("not used")
        override suspend fun share(postId: String, body: ShareRequest): ApiEnvelope<ShareResultDto> = error("not used")
    }

    private fun dto(id: String, replies: Int = 0, reply: CommentDto? = null, parent: String? = null) = CommentDto(
        id = id,
        postId = POST,
        authorId = "other",
        body = "c$id",
        replyCount = replies,
        reply = reply,
        parentId = parent,
        isReply = parent != null,
    )

    private fun controller(
        around: CommentsAroundSource?,
        list: ListApi = ListApi(listOf(dto("plain"))),
    ) = CommentsController(
        POST,
        EngagementRepository(list, mapper),
        CommentActionsRepository(actionsApi, mapper),
        ReportRepository(FakeReportApi(), mapper),
        around = around,
    )

    @Test
    fun `a top-level comment loads the window around it and is marked`() = runTest {
        val asked = mutableListOf<String>()
        val c = controller({ post, comment ->
            asked += "$post/$comment"
            AppResult.Success(listOf(dto("c3"), dto("c2"), dto("c1")))
        })

        val state = c.focus("c2")

        assertThat(asked).containsExactly("$POST/c2")
        assertThat(state.rows.map { it.id }).containsExactly("c3", "c2", "c1").inOrder()
        assertThat(state.focusedCommentId).isEqualTo("c2")
        assertThat(state.focusIndex("c2")).isEqualTo(1)
        // The window has no cursor.
        assertThat(state.nextCursor).isNull()
    }

    @Test
    fun `a reply shown as a preview opens its thread`() = runTest {
        actionsApi.replyPages += FakeCommentActionsApi.page(dto("r1", parent = "p1"))
        val c = controller({ _, _ -> AppResult.Success(listOf(dto("p1", replies = 1, reply = dto("r1", parent = "p1")))) })

        val state = c.focus("r1")

        assertThat(state.thread("p1").expanded).isTrue()
        assertThat(state.focusIndex("r1")).isEqualTo(0)
    }

    @Test
    fun `a reply off screen is found by reading threads, and only its thread stays open`() = runTest {
        actionsApi.replyPages += FakeCommentActionsApi.page(dto("x1", parent = "p1"))
        actionsApi.replyPages += FakeCommentActionsApi.page(dto("target", parent = "p2"))
        val c = controller({ _, _ -> AppResult.Success(listOf(dto("p1", replies = 2), dto("p2", replies = 3), dto("p3", replies = 4))) })

        val state = c.focus("target")

        assertThat(actionsApi.calls).containsExactly("GET /v1/comments/p1/replies", "GET /v1/comments/p2/replies").inOrder()
        assertThat(state.thread("p1").expanded).isFalse()
        assertThat(state.thread("p2").expanded).isTrue()
        assertThat(state.focusIndex("target")).isEqualTo(1)
    }

    @Test
    fun `a comment that is gone falls back to the ordinary list, nothing marked`() = runTest {
        val list = ListApi(listOf(dto("plain")))
        val c = controller({ _, _ -> AppResult.Failure(AppError.NotFound()) }, list)

        val state = c.focus("gone")

        assertThat(list.listed).isEqualTo(1)
        assertThat(state.rows.map { it.id }).containsExactly("plain")
        assertThat(state.focusedCommentId).isNull()
    }

    @Test
    fun `a host with no around source just lists`() = runTest {
        val list = ListApi(listOf(dto("plain")))
        val state = controller(null, list).focus("c1")

        assertThat(list.listed).isEqualTo(1)
        assertThat(state.focusedCommentId).isNull()
    }

    @Test
    fun `a pull to refresh after a focus clears the mark`() = runTest {
        val c = controller({ _, _ -> AppResult.Success(listOf(dto("c1"))) })
        c.focus("c1")

        val state = c.refresh()

        assertThat(state.focusedCommentId).isNull()
    }

    @Test
    fun `the four places a comment can be`() {
        fun row(id: String, replies: Int = 0, reply: CommentRow? = null) =
            CommentRow(id = id, authorId = "a", body = "", createdAt = "", replyCount = replies, reply = reply)

        val preview = CommentRow(id = "r", authorId = "a", body = "", createdAt = "", parentId = "p")
        assertThat(focusTargetIn(listOf(row("c")), "c")).isEqualTo(FocusTarget.Top("c"))
        assertThat(focusTargetIn(listOf(row("p", 1, preview)), "r")).isEqualTo(FocusTarget.Preview("r", "p"))
        // A thread whose one reply is the preview cannot hold another: only threads with more are searched.
        assertThat(focusTargetIn(listOf(row("p", 1, preview), row("q", 2)), "z")).isEqualTo(FocusTarget.Search("z", listOf("q")))
        assertThat(focusTargetIn(listOf(row("p")), "z")).isEqualTo(FocusTarget.Nowhere("z"))
    }

    @Test
    fun `the around route is GET with the comment in the path and a limit`() = runBlocking {
        val server = MockWebServer()
        server.enqueue(
            MockResponse.Builder().code(200).setHeader("Content-Type", "application/json")
                .body("""{"data":[{"id":"c2","post_id":"$POST","author_id":"a","body":"hi","reply_count":0}]}""").build(),
        )
        server.start()
        try {
            val api = Retrofit.Builder().baseUrl(server.url("/")).client(OkHttpClient())
                .addConverterFactory(json.asConverterFactory("application/json".toMediaType())).build()
                .create(CommentsAroundApi::class.java)

            val result = CommentsAroundRepository(api, mapper).around(POST, "c2")

            assertThat((result as AppResult.Success).data.single().id).isEqualTo("c2")
            val sent = server.takeRequest()
            assertThat(sent.url.encodedPath).isEqualTo("/v1/posts/$POST/comments/around/c2")
            assertThat(sent.url.queryParameter("limit")).isEqualTo("20")
        } finally {
            server.close()
        }
    }

    private companion object {
        const val POST = "post-1"
    }
}

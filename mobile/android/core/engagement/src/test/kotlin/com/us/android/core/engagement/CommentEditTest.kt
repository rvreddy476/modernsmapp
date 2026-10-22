package com.us.android.core.engagement

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.engagement.data.BookmarkStatusDto
import com.us.android.core.engagement.data.COMMENT_EDIT_WINDOW
import com.us.android.core.engagement.data.CommentActionsApi
import com.us.android.core.engagement.data.CommentActionsRepository
import com.us.android.core.engagement.data.CommentDto
import com.us.android.core.engagement.data.CommentMenuAction
import com.us.android.core.engagement.data.CommentRow
import com.us.android.core.engagement.data.CommentsController
import com.us.android.core.engagement.data.CommentsUiState
import com.us.android.core.engagement.data.CommentsViewer
import com.us.android.core.engagement.data.CreateCommentRequest
import com.us.android.core.engagement.data.EngagementApi
import com.us.android.core.engagement.data.EngagementRepository
import com.us.android.core.engagement.data.ReactionRequest
import com.us.android.core.engagement.data.ReactionStatusDto
import com.us.android.core.engagement.data.ReportRepository
import com.us.android.core.engagement.data.RepostDto
import com.us.android.core.engagement.data.RepostRequest
import com.us.android.core.engagement.data.ShareRequest
import com.us.android.core.engagement.data.ShareResultDto
import com.us.android.core.engagement.data.commentMenuActions
import com.us.android.core.engagement.data.isWithinEditWindow
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.async
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory
import java.time.Duration
import java.time.Instant

/**
 * Editing a comment (2026-10-22): the route and body post-service's
 * `EditComment` takes, who is offered Edit and for how long (the author,
 * 15 minutes, as the server enforces), and the composer's edit — shown at
 * once, marked "Edited", rolled back with the reason when the server says no.
 */
class CommentEditTest {

    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
    }
    private val mapper = ErrorMapper(json)

    // ── The wire ────────────────────────────────────────────────────────

    @Test
    fun `an edit is a PATCH on the comment with the trimmed body`() {
        val server = MockWebServer()
        server.start()
        try {
            val api = Retrofit.Builder()
                .baseUrl(server.url("/"))
                .client(OkHttpClient())
                .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
                .build()
                .create(CommentActionsApi::class.java)
            server.enqueue(
                MockResponse.Builder().code(200).setHeader("Content-Type", "application/json")
                    .body("""{"data":{"status":"updated"}}""").build(),
            )
            server.enqueue(
                MockResponse.Builder().code(403).setHeader("Content-Type", "application/json")
                    .body("""{"error":{"code":"EDIT_WINDOW_EXPIRED","message":"Comments can only be edited within 15 minutes of creation"}}""")
                    .build(),
            )
            val repository = CommentActionsRepository(api, mapper)

            val ok = runBlocking { repository.edit("c1", "  fixed typo ") }
            val late = runBlocking { repository.edit("c1", "again") }

            val request = server.takeRequest()
            assertThat(request.method).isEqualTo("PATCH")
            assertThat(request.target).isEqualTo("/v1/comments/c1")
            assertThat(request.body?.utf8()).isEqualTo("""{"body":"fixed typo"}""")
            assertThat(ok).isInstanceOf(AppResult.Success::class.java)
            val error = (late as AppResult.Failure).error
            assertThat((error as AppError.Forbidden).code).isEqualTo("EDIT_WINDOW_EXPIRED")
        } finally {
            server.close()
        }
    }

    // ── Who may edit, and for how long ─────────────────────────────────

    private val now: Instant = Instant.parse("2026-10-22T12:00:00Z")

    private fun row(author: String, minutesAgo: Long?) = CommentRow(
        id = "c",
        authorId = author,
        body = "b",
        createdAt = minutesAgo?.let { now.minus(Duration.ofMinutes(it)).toString() }.orEmpty(),
    )

    @Test
    fun `the author is offered Edit for fifteen minutes, then not`() {
        assertThat(commentMenuActions(row("me", 3), "me", "alice", now)).contains(CommentMenuAction.EDIT)
        assertThat(commentMenuActions(row("me", 14), "me", "alice", now)).contains(CommentMenuAction.EDIT)
        assertThat(commentMenuActions(row("me", 15), "me", "alice", now)).doesNotContain(CommentMenuAction.EDIT)
        assertThat(commentMenuActions(row("me", 90), "me", "alice", now)).doesNotContain(CommentMenuAction.EDIT)
    }

    @Test
    fun `nobody else is offered Edit, the post's creator included`() {
        assertThat(commentMenuActions(row("bob", 1), "me", "me", now)).doesNotContain(CommentMenuAction.EDIT)
        assertThat(commentMenuActions(row("bob", 1), "me", "alice", now)).doesNotContain(CommentMenuAction.EDIT)
    }

    @Test
    fun `the menu stays alphabetical with Edit in it`() {
        assertThat(commentMenuActions(row("me", 1), "me", "me", now).map { it.label })
            .containsExactly("Delete", "Edit", "Heart", "Pin").inOrder()
    }

    @Test
    fun `a time that cannot be read is not editable, a slightly future one is`() {
        assertThat(isWithinEditWindow("", now)).isFalse()
        assertThat(isWithinEditWindow("yesterday", now)).isFalse()
        assertThat(isWithinEditWindow(now.plusSeconds(5).toString(), now)).isTrue()
        assertThat(COMMENT_EDIT_WINDOW).isEqualTo(Duration.ofMinutes(15))
    }

    // ── The composer's edit ─────────────────────────────────────────────

    private class ListApi(private val rows: List<CommentDto>) : EngagementApi {
        override suspend fun getComments(postId: String, limit: Int, cursor: String?) = ApiEnvelope(data = rows)
        override suspend fun addComment(postId: String, idempotencyKey: String, body: CreateCommentRequest) =
            ApiEnvelope(data = CommentDto(id = "new", postId = postId, authorId = "me", body = body.text))

        override suspend fun addReaction(postId: String, body: ReactionRequest): ApiEnvelope<ReactionStatusDto> =
            error("not used")

        override suspend fun removeReaction(postId: String): ApiEnvelope<ReactionStatusDto> = error("not used")
        override suspend fun addBookmark(postId: String): ApiEnvelope<BookmarkStatusDto> = error("not used")
        override suspend fun removeBookmark(postId: String): ApiEnvelope<BookmarkStatusDto> = error("not used")
        override suspend fun repost(postId: String, body: RepostRequest): ApiEnvelope<RepostDto> = error("not used")
        override suspend fun removeRepost(postId: String) = error("not used")
        override suspend fun share(postId: String, body: ShareRequest): ApiEnvelope<ShareResultDto> =
            error("not used")
    }

    private val actionsApi = FakeCommentActionsApi()

    private suspend fun controller(): CommentsController = CommentsController(
        "p1",
        EngagementRepository(
            ListApi(
                listOf(
                    CommentDto(id = "c1", postId = "p1", authorId = "me", body = "helo"),
                    CommentDto(id = "c2", postId = "p1", authorId = "bob", body = "hi"),
                ),
            ),
            mapper,
        ),
        CommentActionsRepository(actionsApi, mapper),
        ReportRepository(FakeReportApi(), mapper),
    ).apply {
        setViewer(CommentsViewer(id = "me", name = "Me"))
        refresh()
    }

    private fun CommentsUiState.row(id: String) = rows.first { it.id == id }

    @Test
    fun `edit puts the comment's text in the composer`() = runTest {
        val controller = controller()

        val state = controller.startEdit("c1")

        assertThat(state.editing?.commentId).isEqualTo("c1")
        assertThat(state.draft).isEqualTo("helo")
        assertThat(state.replyingTo).isNull()
    }

    @Test
    fun `saving shows the new text at once, marked Edited, and sends it`() = runTest {
        val gate = CompletableDeferred<Unit>()
        actionsApi.answers["PATCH /v1/comments/c1 hello"] = {
            gate.await()
            ApiEnvelope(data = com.us.android.core.engagement.data.CommentStatusDto("updated"))
        }
        val controller = controller()
        controller.startEdit("c1")
        controller.onDraftChange("hello ")

        val pending = async { controller.submit() }
        testScheduler.runCurrent()
        val during = controller.snapshot()
        assertThat(during.row("c1").body).isEqualTo("hello")
        assertThat(during.row("c1").edited).isTrue()
        assertThat(during.submitting).isTrue()

        gate.complete(Unit)
        val after = pending.await()
        assertThat(actionsApi.calls).contains("PATCH /v1/comments/c1 hello")
        assertThat(after.editing).isNull()
        assertThat(after.draft).isEmpty()
        assertThat(after.row("c1").edited).isTrue()
    }

    @Test
    fun `a refused edit puts the old text back and keeps what was typed, with the reason`() = runTest {
        actionsApi.answers["PATCH /v1/comments/c1 hello"] = {
            throw HttpException(
                Response.error<Any>(
                    403,
                    """{"error":{"code":"EDIT_WINDOW_EXPIRED","message":"late"}}""".toResponseBody("application/json".toMediaType()),
                ),
            )
        }
        val controller = controller()
        controller.startEdit("c1")
        controller.onDraftChange("hello")

        val state = controller.submit()

        assertThat(state.row("c1").body).isEqualTo("helo")
        assertThat(state.row("c1").edited).isFalse()
        assertThat(state.editing?.commentId).isEqualTo("c1")
        assertThat(state.draft).isEqualTo("hello")
        assertThat((state.submitError as AppError.Forbidden).code).isEqualTo("EDIT_WINDOW_EXPIRED")
    }

    @Test
    fun `the same text as before sends nothing and closes the edit`() = runTest {
        val controller = controller()
        controller.startEdit("c1")

        val state = controller.submit()

        assertThat(actionsApi.calls).isEmpty()
        assertThat(state.editing).isNull()
        assertThat(state.row("c1").edited).isFalse()
    }

    @Test
    fun `cancel leaves the comment as it was and empties the composer`() = runTest {
        val controller = controller()
        controller.startEdit("c1")
        controller.onDraftChange("something else")

        val state = controller.cancelEdit()

        assertThat(state.editing).isNull()
        assertThat(state.draft).isEmpty()
        assertThat(state.row("c1").body).isEqualTo("helo")
    }

    /** The server keeps no edited flag: a refresh must not wipe the mark off a row edited here. */
    @Test
    fun `an edited row is still marked after the list is read again`() = runTest {
        val controller = controller()
        controller.startEdit("c1")
        controller.onDraftChange("hello")
        controller.submit()

        val state = controller.refresh()

        assertThat(state.row("c1").edited).isTrue()
        assertThat(state.row("c2").edited).isFalse()
    }

    @Test
    fun `starting a reply drops an edit in progress`() = runTest {
        val controller = controller()
        controller.startEdit("c1")

        val state = controller.startReply("c2")

        assertThat(state.editing).isNull()
        assertThat(state.replyingTo?.parentId).isEqualTo("c2")
        assertThat(state.draft).isEmpty()
    }
}

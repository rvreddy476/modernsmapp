package com.us.android.core.engagement

import com.google.common.truth.Truth.assertThat
import com.us.android.core.engagement.data.BookmarkStatusDto
import com.us.android.core.engagement.data.CommentActionsRepository
import com.us.android.core.engagement.data.CommentAuthorDto
import com.us.android.core.engagement.data.CommentDto
import com.us.android.core.engagement.data.CommentMentionSource
import com.us.android.core.engagement.data.CommentNotice
import com.us.android.core.engagement.data.CommentReactionSummaryDto
import com.us.android.core.engagement.data.CommentReportStatus
import com.us.android.core.engagement.data.CommentsController
import com.us.android.core.engagement.data.CommentsUiState
import com.us.android.core.engagement.data.CommentsViewer
import com.us.android.core.engagement.data.CreateCommentRequest
import com.us.android.core.engagement.data.EngagementApi
import com.us.android.core.engagement.data.EngagementRepository
import com.us.android.core.engagement.data.MentionCandidate
import com.us.android.core.engagement.data.ReactionRequest
import com.us.android.core.engagement.data.ReactionStatusDto
import com.us.android.core.engagement.data.ReportRepository
import com.us.android.core.engagement.data.RepostDto
import com.us.android.core.engagement.data.RepostRequest
import com.us.android.core.engagement.data.ShareRequest
import com.us.android.core.engagement.data.ShareResultDto
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response
import java.io.IOException

/**
 * The comment row actions (2026-10-14, audit T-17): reply threading, the
 * optimistic like and its rollback, delete, the creator's pin and heart,
 * reporting, and `@` suggestions.
 *
 * Driven through the real repositories against scripted APIs, so the
 * envelope handling and the exact route each action calls are exercised.
 */
class CommentActionsControllerTest {

    private val json = Json { ignoreUnknownKeys = true }
    private val mapper = ErrorMapper(json)

    /** Serves one page of top-level comments; nothing else is used here. */
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
    private val reportApi = FakeReportApi()

    private fun dto(
        id: String,
        author: String = "other",
        username: String = "",
        likes: Int = 0,
        viewerReaction: String? = null,
        replies: Int = 0,
        pinned: Boolean = false,
        reply: CommentDto? = null,
    ) = CommentDto(
        id = id,
        postId = POST,
        authorId = author,
        body = "c$id",
        likeCount = likes,
        reactionCount = likes,
        viewerReaction = viewerReaction,
        replyCount = replies,
        pinned = pinned,
        author = CommentAuthorDto(id = author, username = username, displayName = username.uppercase()),
        reply = reply,
    )

    private suspend fun controller(
        vararg rows: CommentDto,
        mentions: CommentMentionSource = CommentMentionSource.None,
    ): CommentsController = CommentsController(
        POST,
        EngagementRepository(ListApi(rows.toList()), mapper),
        CommentActionsRepository(actionsApi, mapper),
        ReportRepository(reportApi, mapper),
        mentions,
    ).apply {
        setViewer(CommentsViewer(id = "me", name = "Me"))
        refresh()
    }

    private fun CommentsUiState.row(id: String) =
        rows.firstOrNull { it.id == id }
            ?: threads.values.flatMap { it.replies }.first { it.id == id }

    private fun httpError(code: Int, errorCode: String = "X"): Nothing =
        throw HttpException(Response.error<Any>(code, """{"error":{"code":"$errorCode"}}""".toResponseBody()))

    // ── Like ────────────────────────────────────────────────────────────

    @Test
    fun `a like shows at once, sends PUT reaction heart, and takes the server count`() = runTest {
        val controller = controller(dto("c1", likes = 4))
        var seenDuringRequest: CommentsUiState? = null
        actionsApi.answers["PUT /v1/comments/c1/reaction ❤️"] = {
            seenDuringRequest = controller.snapshot()
            ApiEnvelope(data = CommentReactionSummaryDto(emoji = "❤️", reactionCount = 9, viewerReaction = "❤️"))
        }

        val state = controller.toggleLike("c1")

        val seen = checkNotNull(seenDuringRequest)
        assertThat(seen.row("c1").viewerLiked).isTrue()
        assertThat(seen.row("c1").likeCount).isEqualTo(5)
        assertThat(state.row("c1").viewerLiked).isTrue()
        assertThat(state.row("c1").likeCount).isEqualTo(9)
        assertThat(actionsApi.calls).containsExactly("PUT /v1/comments/c1/reaction ❤️")
    }

    @Test
    fun `a failed like is rolled back to the row's own previous state, with a notice`() = runTest {
        val controller = controller(dto("c1", likes = 4))
        actionsApi.answers["PUT /v1/comments/c1/reaction ❤️"] = { throw IOException("offline") }

        val state = controller.toggleLike("c1")

        assertThat(state.row("c1").viewerLiked).isFalse()
        assertThat(state.row("c1").likeCount).isEqualTo(4)
        assertThat(state.notice).isEqualTo(CommentNotice.LIKE)
    }

    @Test
    fun `unliking sends DELETE reaction and a failed unlike restores the like`() = runTest {
        val controller = controller(dto("c1", likes = 3, viewerReaction = "🔥"))
        assertThat(controller.snapshot().row("c1").viewerLiked).isTrue()
        actionsApi.answers["DELETE /v1/comments/c1/reaction"] = { httpError(500) }

        val state = controller.toggleLike("c1")

        assertThat(actionsApi.calls).containsExactly("DELETE /v1/comments/c1/reaction")
        assertThat(state.row("c1").viewerLiked).isTrue()
        assertThat(state.row("c1").likeCount).isEqualTo(3)
    }

    @Test
    fun `a second tap while the first like is on the wire sends nothing`() = runTest {
        val controller = controller(dto("c1"))
        actionsApi.answers["PUT /v1/comments/c1/reaction ❤️"] = {
            controller.toggleLike("c1") // the impatient second tap
            ApiEnvelope(data = CommentReactionSummaryDto(reactionCount = 1, viewerReaction = "❤️"))
        }

        controller.toggleLike("c1")

        assertThat(actionsApi.calls).containsExactly("PUT /v1/comments/c1/reaction ❤️")
    }

    @Test
    fun `liking a reply updates it in its thread`() = runTest {
        val controller = controller(dto("c1", replies = 1))
        actionsApi.replyPages += FakeCommentActionsApi.page(dto("r1"))
        controller.toggleReplies("c1")

        val state = controller.toggleLike("r1")

        assertThat(state.thread("c1").replies.single().viewerLiked).isTrue()
    }

    // ── Replies ─────────────────────────────────────────────────────────

    @Test
    fun `opening replies fetches the first page once, then pages by cursor`() = runTest {
        val controller = controller(dto("c1", replies = 3))
        actionsApi.replyPages += FakeCommentActionsApi.page(dto("r1"), dto("r2"), next = "cur-1")
        actionsApi.replyPages += FakeCommentActionsApi.page(dto("r2"), dto("r3"))

        var state = controller.toggleReplies("c1")
        assertThat(state.thread("c1").expanded).isTrue()
        assertThat(state.thread("c1").replies.map { it.id }).containsExactly("r1", "r2").inOrder()
        assertThat(state.thread("c1").replies.all { it.parentId == "c1" }).isTrue()

        state = controller.loadReplies("c1")
        assertThat(state.thread("c1").replies.map { it.id }).containsExactly("r1", "r2", "r3").inOrder()
        assertThat(actionsApi.replyCursors).containsExactly(null, "cur-1").inOrder()

        state = controller.toggleReplies("c1")
        assertThat(state.thread("c1").expanded).isFalse()
        state = controller.toggleReplies("c1")
        assertThat(state.thread("c1").expanded).isTrue()
        // Folding and unfolding does not refetch.
        assertThat(actionsApi.replyCursors).hasSize(2)
    }

    @Test
    fun `a failed replies page keeps the thread open with its error`() = runTest {
        val controller = controller(dto("c1", replies = 2))
        actionsApi.replyPages += { throw IOException("offline") }

        val state = controller.toggleReplies("c1")

        assertThat(state.thread("c1").expanded).isTrue()
        assertThat(state.thread("c1").error).isNotNull()
        assertThat(state.thread("c1").loaded).isFalse()
    }

    @Test
    fun `replying to a comment posts to it and threads the reply under it`() = runTest {
        val controller = controller(dto("c1", replies = 0))

        controller.startReply("c1")
        assertThat(controller.snapshot().replyingTo?.parentId).isEqualTo("c1")
        controller.onDraftChange("agreed")
        val state = controller.submit()

        assertThat(actionsApi.calls).containsExactly("POST /v1/comments/c1/reply agreed")
        assertThat(actionsApi.replyKeys.single()).isNotEmpty()
        assertThat(state.thread("c1").expanded).isTrue()
        val reply = state.thread("c1").replies.single()
        assertThat(reply.id).isEqualTo("r-new")
        assertThat(reply.parentId).isEqualTo("c1")
        assertThat(reply.authorName).isEqualTo("Me")
        assertThat(state.row("c1").replyCount).isEqualTo(1)
        assertThat(state.replyingTo).isNull()
        assertThat(state.draft).isEmpty()
        // Not a top-level comment.
        assertThat(state.rows.map { it.id }).containsExactly("c1")
    }

    @Test
    fun `replying to a reply goes to the same thread and names the person in the draft`() = runTest {
        val controller = controller(
            dto("c1", replies = 1, reply = dto("r1", author = "bob", username = "bob").copy(isReply = true)),
        )

        val started = controller.startReply("r1")

        assertThat(started.replyingTo?.parentId).isEqualTo("c1")
        assertThat(started.replyingTo?.targetId).isEqualTo("r1")
        assertThat(started.replyingTo?.name).isEqualTo("@bob")
        assertThat(started.draft).isEqualTo("@bob ")

        controller.onDraftChange("@bob same here")
        controller.submit()
        assertThat(actionsApi.calls).containsExactly("POST /v1/comments/c1/reply @bob same here")
    }

    @Test
    fun `cancelling a reply drops an untouched prefill but keeps typed words`() = runTest {
        val controller = controller(
            dto("c1", replies = 1, reply = dto("r1", author = "bob", username = "bob").copy(isReply = true)),
        )
        controller.startReply("r1")
        assertThat(controller.cancelReply().draft).isEmpty()

        controller.startReply("r1")
        controller.onDraftChange("@bob wait")
        val state = controller.cancelReply()
        assertThat(state.replyingTo).isNull()
        assertThat(state.draft).isEqualTo("@bob wait")
    }

    @Test
    fun `a failed reply keeps the draft and the target, and the retry reuses its key`() = runTest {
        val controller = controller(dto("c1"))
        var attempts = 0
        actionsApi.answers["POST /v1/comments/c1/reply hello"] = {
            attempts++
            if (attempts == 1) throw IOException("lost")
            ApiEnvelope(data = CommentDto(id = "r9", parentId = "c1", isReply = true, body = "hello"))
        }
        controller.startReply("c1")
        controller.onDraftChange("hello")

        val failed = controller.submit()
        assertThat(failed.submitError).isNotNull()
        assertThat(failed.draft).isEqualTo("hello")
        assertThat(failed.replyingTo?.parentId).isEqualTo("c1")
        assertThat(failed.thread("c1").replies).isEmpty()

        val retried = controller.submit()
        assertThat(retried.thread("c1").replies.map { it.id }).containsExactly("r9")
        assertThat(actionsApi.replyKeys).hasSize(2)
        assertThat(actionsApi.replyKeys.distinct()).hasSize(1)
    }

    @Test
    fun `the same words as a top-level comment are a new intent, not the reply's retry`() = runTest {
        val controller = controller(dto("c1"))
        actionsApi.answers["POST /v1/comments/c1/reply hey"] = { throw IOException("lost") }
        controller.startReply("c1")
        controller.onDraftChange("hey")
        controller.submit()
        val replyKey = actionsApi.replyKeys.single()

        controller.cancelReply()
        val state = controller.submit()

        assertThat(state.rows.first().id).isEqualTo("new")
        assertThat(replyKey).isNotEmpty()
    }

    // ── Delete ──────────────────────────────────────────────────────────

    @Test
    fun `deleting your comment removes it and calls DELETE`() = runTest {
        val controller = controller(dto("c1", author = "me"), dto("c2"))

        val state = controller.delete("c1")

        assertThat(actionsApi.calls).containsExactly("DELETE /v1/comments/c1")
        assertThat(state.rows.map { it.id }).containsExactly("c2")
        assertThat(state.notice).isNull()
    }

    @Test
    fun `a refused delete puts the comment back where it was`() = runTest {
        val controller = controller(dto("c0"), dto("c1", author = "me"), dto("c2"))
        actionsApi.answers["DELETE /v1/comments/c1"] = { httpError(403) }

        val state = controller.delete("c1")

        assertThat(state.rows.map { it.id }).containsExactly("c0", "c1", "c2").inOrder()
        assertThat(state.notice).isEqualTo(CommentNotice.DELETE)
    }

    @Test
    fun `a delete answered 404 leaves the comment gone`() = runTest {
        val controller = controller(dto("c1", author = "me"))
        actionsApi.answers["DELETE /v1/comments/c1"] = { httpError(404) }

        assertThat(controller.delete("c1").rows).isEmpty()
    }

    @Test
    fun `deleting your reply takes it out of the thread and the count`() = runTest {
        val controller = controller(dto("c1", replies = 2))
        actionsApi.replyPages += FakeCommentActionsApi.page(dto("r1", author = "me"), dto("r2"))
        controller.toggleReplies("c1")

        val state = controller.delete("r1")

        assertThat(state.thread("c1").replies.map { it.id }).containsExactly("r2")
        assertThat(state.row("c1").replyCount).isEqualTo(1)
    }

    // ── Creator tools ───────────────────────────────────────────────────

    @Test
    fun `pinning moves the comment first and unpins the previous one`() = runTest {
        val controller = controller(dto("c1", pinned = true), dto("c2"), dto("c3"))
        controller.setPostAuthor("me")

        val state = controller.togglePin("c3")

        assertThat(actionsApi.calls).containsExactly("PUT /v1/comments/c3/pin")
        assertThat(state.rows.map { it.id }).containsExactly("c3", "c1", "c2").inOrder()
        assertThat(state.rows.filter { it.pinned }.map { it.id }).containsExactly("c3")
    }

    @Test
    fun `a refused pin restores the old pin and the old order`() = runTest {
        val controller = controller(dto("c1", pinned = true), dto("c2"), dto("c3"))
        actionsApi.answers["PUT /v1/comments/c3/pin"] = { httpError(403) }

        val state = controller.togglePin("c3")

        assertThat(state.rows.map { it.id }).containsExactly("c1", "c2", "c3").inOrder()
        assertThat(state.rows.filter { it.pinned }.map { it.id }).containsExactly("c1")
        assertThat(state.notice).isEqualTo(CommentNotice.PIN)
    }

    @Test
    fun `unpinning sends DELETE pin`() = runTest {
        val controller = controller(dto("c1", pinned = true))

        val state = controller.togglePin("c1")

        assertThat(actionsApi.calls).containsExactly("DELETE /v1/comments/c1/pin")
        assertThat(state.row("c1").pinned).isFalse()
    }

    @Test
    fun `a heart shows at once and is undone when refused`() = runTest {
        val controller = controller(dto("c1"))
        val hearted = controller.toggleHeart("c1")
        assertThat(actionsApi.calls).containsExactly("POST /v1/comments/c1/heart")
        assertThat(hearted.row("c1").heartedByAuthor).isTrue()

        actionsApi.answers["DELETE /v1/comments/c1/heart"] = { throw IOException("offline") }
        val refused = controller.toggleHeart("c1")
        assertThat(refused.row("c1").heartedByAuthor).isTrue()
        assertThat(refused.notice).isEqualTo(CommentNotice.HEART)
    }

    // ── Report ──────────────────────────────────────────────────────────

    @Test
    fun `a report files a comment report with trust-safety`() = runTest {
        val controller = controller(dto("c1"))

        val state = controller.report("c1", "spam", "")

        assertThat(state.report).isEqualTo(CommentReportStatus.SENT)
        val filed = reportApi.filed.single()
        assertThat(filed.entityType).isEqualTo("comment")
        assertThat(filed.entityId).isEqualTo("c1")
        assertThat(filed.reason).isEqualTo("spam")
        assertThat(controller.resetReport().report).isEqualTo(CommentReportStatus.IDLE)
    }

    @Test
    fun `a second report on the same comment reads as already reported`() = runTest {
        val controller = controller(dto("c1"))

        // trust-safety's 409 for a report already open on the comment.
        reportApi.answer = {
            httpError(409, "ACTIVE_REPORT_EXISTS")
        }
        assertThat(controller.report("c1", "spam", "").report).isEqualTo(CommentReportStatus.ALREADY_REPORTED)

        reportApi.answer = { throw IOException("offline") }
        assertThat(controller.report("c1", "spam", "").report).isEqualTo(CommentReportStatus.FAILED)
    }

    // ── @mentions ───────────────────────────────────────────────────────

    @Test
    fun `typing an at-handle asks for suggestions and picking one completes it`() = runTest {
        val asked = mutableListOf<String>()
        val controller = controller(
            dto("c1"),
            mentions = CommentMentionSource { q ->
                asked += q
                listOf(
                    MentionCandidate("u1", "raghu", "Raghu"),
                    MentionCandidate("u2", "", "No handle"),
                )
            },
        )

        assertThat(controller.onDraftChange("great point @ra").mentionQuery).isEqualTo("ra")
        val suggested = controller.searchMentions()
        assertThat(asked).containsExactly("ra")
        assertThat(suggested.mentions.map { it.username }).containsExactly("raghu")

        val picked = controller.pickMention(suggested.mentions.single())
        assertThat(picked.draft).isEqualTo("great point @raghu ")
        assertThat(picked.mentions).isEmpty()
        assertThat(picked.mentionQuery).isNull()
    }

    @Test
    fun `suggestions for a handle no longer being typed are dropped`() = runTest {
        lateinit var subject: CommentsController
        subject = controller(
            dto("c1"),
            mentions = CommentMentionSource {
                subject.onDraftChange("finished typing ")
                listOf(MentionCandidate("u1", "raghu", "Raghu"))
            },
        )
        subject.onDraftChange("@ra")

        assertThat(subject.searchMentions().mentions).isEmpty()
    }

    private companion object {
        const val POST = "p1"
    }
}

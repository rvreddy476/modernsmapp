package com.us.android.core.engagement

import com.us.android.core.engagement.data.CommentActionsApi
import com.us.android.core.engagement.data.CommentDto
import com.us.android.core.engagement.data.CommentHeartDto
import com.us.android.core.engagement.data.CommentPinDto
import com.us.android.core.engagement.data.CommentReactionRequest
import com.us.android.core.engagement.data.CommentReactionSummaryDto
import com.us.android.core.engagement.data.CommentStatusDto
import com.us.android.core.engagement.data.CreateCommentRequest
import com.us.android.core.engagement.data.FileReportRequest
import com.us.android.core.engagement.data.ReportApi
import com.us.android.core.engagement.data.ReportDto
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ApiMeta
import kotlinx.coroutines.CompletableDeferred

/**
 * A scriptable [CommentActionsApi]: every call is recorded as "METHOD path"
 * (with its body where it has one), and answers from [answer] — which may
 * suspend on a [CompletableDeferred] so a test can look at the optimistic
 * state while the request is still "on the wire".
 */
internal class FakeCommentActionsApi : CommentActionsApi {

    val calls = mutableListOf<String>()
    val replyKeys = mutableListOf<String>()

    /** Per-call answers by "METHOD path"; a missing one answers a plain success. */
    val answers = mutableMapOf<String, suspend () -> Any>()

    /** Queued replies pages for GET …/replies, consumed in order. */
    val replyPages = ArrayDeque<() -> ApiEnvelope<List<CommentDto>>>()
    val replyCursors = mutableListOf<String?>()

    @Suppress("UNCHECKED_CAST")
    private suspend fun <T> answer(call: String, default: () -> T): T {
        calls += call
        val scripted = answers[call] ?: return default()
        return scripted() as T
    }

    override suspend fun replies(commentId: String, limit: Int, cursor: String?): ApiEnvelope<List<CommentDto>> {
        calls += "GET /v1/comments/$commentId/replies"
        replyCursors += cursor
        return replyPages.removeFirst().invoke()
    }

    override suspend fun reply(
        commentId: String,
        idempotencyKey: String,
        body: CreateCommentRequest,
    ): ApiEnvelope<CommentDto> {
        replyKeys += idempotencyKey
        return answer("POST /v1/comments/$commentId/reply ${body.text}") {
            ApiEnvelope(data = CommentDto(id = "r-new", parentId = commentId, isReply = true, body = body.text))
        }
    }

    override suspend fun setReaction(
        commentId: String,
        body: CommentReactionRequest,
    ): ApiEnvelope<CommentReactionSummaryDto> = answer("PUT /v1/comments/$commentId/reaction ${body.emoji}") {
        ApiEnvelope(data = CommentReactionSummaryDto(emoji = body.emoji, reactionCount = 1, viewerReaction = body.emoji))
    }

    override suspend fun removeReaction(commentId: String): ApiEnvelope<CommentReactionSummaryDto> =
        answer("DELETE /v1/comments/$commentId/reaction") {
            ApiEnvelope(data = CommentReactionSummaryDto(reactionCount = 0))
        }

    override suspend fun delete(commentId: String): ApiEnvelope<CommentStatusDto> =
        answer("DELETE /v1/comments/$commentId") { ApiEnvelope(data = CommentStatusDto("deleted")) }

    override suspend fun heart(commentId: String): ApiEnvelope<CommentHeartDto> =
        answer("POST /v1/comments/$commentId/heart") { ApiEnvelope(data = CommentHeartDto(commentId, true)) }

    override suspend fun unheart(commentId: String): ApiEnvelope<CommentHeartDto> =
        answer("DELETE /v1/comments/$commentId/heart") { ApiEnvelope(data = CommentHeartDto(commentId, false)) }

    override suspend fun pin(commentId: String): ApiEnvelope<CommentPinDto> =
        answer("PUT /v1/comments/$commentId/pin") { ApiEnvelope(data = CommentPinDto(commentId, "p1", true)) }

    override suspend fun unpin(commentId: String): ApiEnvelope<CommentPinDto> =
        answer("DELETE /v1/comments/$commentId/pin") { ApiEnvelope(data = CommentPinDto(commentId, "p1", false)) }

    companion object {
        fun page(vararg replies: CommentDto, next: String? = null): () -> ApiEnvelope<List<CommentDto>> =
            { ApiEnvelope(data = replies.toList(), meta = ApiMeta(nextCursor = next)) }
    }
}

/** A [ReportApi] that records what was filed and answers [answer]. */
internal class FakeReportApi(
    var answer: suspend () -> ApiEnvelope<ReportDto> = { ApiEnvelope(data = ReportDto(id = "rep", status = "open")) },
) : ReportApi {
    val filed = mutableListOf<FileReportRequest>()

    override suspend fun fileReport(body: FileReportRequest): ApiEnvelope<ReportDto> {
        filed += body
        return answer()
    }
}

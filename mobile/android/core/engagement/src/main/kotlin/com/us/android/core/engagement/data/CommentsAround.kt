package com.us.android.core.engagement.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.listApiCall
import retrofit2.http.GET
import retrofit2.http.Path
import retrofit2.http.Query
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The thread opened ON a comment (2026-10-24, the web's useCommentsAround):
 * a comment or mention notification links `/post/<id>?focusComment=<id>`,
 * and post-service answers the TOP-LEVEL comments around it —
 * `GET /v1/posts/:postId/comments/around/:commentId?limit=` (internal/http
 * handler.go:154, ListCommentsAround :1804-1850; store GetCommentsAround,
 * comments.go:498: half newer, the target, half older, newest first, the
 * same `Comment` rows as the list). 404 NOT_FOUND when the comment is gone.
 *
 * Its own interface so the [EngagementApi] fakes keep their shape.
 */
interface CommentsAroundApi {
    @GET("v1/posts/{postId}/comments/around/{commentId}")
    suspend fun around(
        @Path("postId") postId: String,
        @Path("commentId") commentId: String,
        @Query("limit") limit: Int,
    ): ApiEnvelope<List<CommentDto>>
}

/** The read [CommentsController.focus] uses; a seam so the controller tests with a fake. */
fun interface CommentsAroundSource {
    suspend fun around(postId: String, commentId: String): AppResult<List<CommentDto>>
}

@Singleton
class CommentsAroundRepository @Inject constructor(
    private val api: CommentsAroundApi,
    private val errorMapper: ErrorMapper,
) : CommentsAroundSource {
    override suspend fun around(postId: String, commentId: String): AppResult<List<CommentDto>> =
        listApiCall(errorMapper) { api.around(postId, commentId, AROUND_LIMIT) }

    private companion object {
        /** The server's own default (and at most 50). */
        const val AROUND_LIMIT = 20
    }
}

/** Where the focused comment sits in a loaded list (the web's focusComment.ts `focusTarget`). */
sealed interface FocusTarget {
    /** A top-level row of the list. */
    data class Top(val id: String) : FocusTarget

    /** The first-reply preview under [parentId], already on screen. */
    data class Preview(val id: String, val parentId: String) : FocusTarget

    /** Not on screen: look through the replies of these parents, in order. */
    data class Search(val id: String, val parents: List<String>) : FocusTarget

    /** No thread in the list could hold it. */
    data class Nowhere(val id: String) : FocusTarget
}

/** Threads searched for a reply target, at most (focusComment.ts MAX_FOCUS_PARENTS). */
const val MAX_FOCUS_PARENTS = 8

/** Pure, so the four cases are a table test. */
fun focusTargetIn(rows: List<CommentRow>, id: String): FocusTarget {
    if (rows.any { it.id == id }) return FocusTarget.Top(id)
    rows.firstOrNull { it.reply?.id == id }?.let { return FocusTarget.Preview(id, it.id) }
    val parents = rows
        .filter { it.replyCount > 1 || (it.replyCount > 0 && it.reply == null) }
        .take(MAX_FOCUS_PARENTS)
        .map { it.id }
    return if (parents.isEmpty()) FocusTarget.Nowhere(id) else FocusTarget.Search(id, parents)
}

/**
 * The index in [rows] the list scrolls to for [id]: its own row, or the
 * thread it is a reply in (an open thread, or a preview); null when absent.
 */
fun CommentsUiState.focusIndex(id: String): Int? =
    rows.indexOfFirst { row ->
        row.id == id || row.reply?.id == id || thread(row.id).replies.any { it.id == id }
    }.takeIf { it >= 0 }

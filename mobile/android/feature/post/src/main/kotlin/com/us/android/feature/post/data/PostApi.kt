package com.us.android.feature.post.data

import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.retry.Retryable
import com.us.android.feature.post.data.dto.BookmarkStatusDto
import com.us.android.feature.post.data.dto.CommentDto
import com.us.android.feature.post.data.dto.PostDto
import com.us.android.feature.post.data.dto.ReactionRequest
import com.us.android.feature.post.data.dto.ReactionStatusDto
import com.us.android.feature.post.data.dto.RepostDto
import com.us.android.feature.post.data.dto.RepostRequest
import retrofit2.http.Body
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.POST
import retrofit2.http.Path
import retrofit2.http.Query

/**
 * Post and interaction endpoints.
 *
 * A Retrofit interface, not a client: created from the app-wide `Retrofit`, so
 * it inherits the base URL, auth, single-flight refresh, tracing and error
 * mapping. Every path here was exercised live through the gateway; see
 * prompt/android-api-contracts.md §2, and treat its 2026-08-17 sections as
 * canonical where they supersede the original capture.
 *
 * Retry is opt-in for non-GET methods. Only the bookmark pair carries
 * `@Retryable`, because only that pair has been proven idempotent by repeated
 * live calls. Reactions and reposts do not honour an idempotency key, so a
 * replayed one is a second write rather than a no-op.
 */
interface PostApi {

    /** Public read. 404 `NOT_FOUND` when absent. */
    @GET("v1/posts/{postId}")
    suspend fun getPost(@Path("postId") postId: String): ApiEnvelope<PostDto>

    @POST("v1/posts/{postId}/reactions")
    suspend fun addReaction(
        @Path("postId") postId: String,
        @Body body: ReactionRequest,
    ): ApiEnvelope<ReactionStatusDto>

    /** No request body — the reaction to remove is implied by the viewer. */
    @DELETE("v1/posts/{postId}/reactions")
    suspend fun removeReaction(
        @Path("postId") postId: String,
    ): ApiEnvelope<ReactionStatusDto>

    /**
     * Saves the post. SET semantics, not a toggle.
     *
     * Repaired on 2026-08-17 and recaptured: two consecutive POSTs both return
     * `{"bookmarked":true}`, and the saved list reflects one entry. Before the
     * repair this was a toggle whose reversal endpoint was broken, so a
     * replayed call silently un-saved the post — which is why this pair is
     * annotated `@Retryable` only now that repetition is proven harmless.
     */
    @Retryable
    @POST("v1/posts/{postId}/bookmark")
    suspend fun setBookmark(
        @Path("postId") postId: String,
    ): ApiEnvelope<BookmarkStatusDto>

    /**
     * Removes the post from saved items. CLEAR semantics, also idempotent —
     * two consecutive DELETEs both return `{"bookmarked":false}` and the list
     * is empty afterwards.
     */
    @Retryable
    @DELETE("v1/posts/{postId}/bookmark")
    suspend fun clearBookmark(
        @Path("postId") postId: String,
    ): ApiEnvelope<BookmarkStatusDto>

    /** Returns HTTP 201 with the created repost. */
    @POST("v1/posts/{postId}/repost")
    suspend fun repost(
        @Path("postId") postId: String,
        @Body body: RepostRequest,
    ): ApiEnvelope<RepostDto>

    /**
     * Returns `204` with an empty body — no envelope.
     *
     * The `Unit` return type is required: Retrofit skips body conversion, and
     * the caller routes this through `noContentApiCall` rather than `apiCall`,
     * which would treat the absent `data` as a malformed response and turn
     * every successful delete into a failure.
     */
    @DELETE("v1/posts/{postId}/repost")
    suspend fun removeRepost(@Path("postId") postId: String)

    /**
     * One page of comments. Public — the capture succeeded with no token, and
     * again with one.
     *
     * This route was previously declared unbuildable because the 2026-08-16
     * capture only ever returned `{"data":[]}`. The 2026-08-17 recapture
     * returned a populated item, so the shape is now evidence rather than
     * guesswork.
     *
     * It returns exactly ONE page and there is no way to ask for a second.
     * [limit] is honoured — it is the only query parameter ever sent — but the
     * non-empty `limit=1` response carried no `meta` and no `next_cursor`, so
     * nothing in the contract says how to advance. The return type is a bare
     * `List`, not `Paged`, so no caller can be misled into building a "load
     * more" that has no cursor to send.
     *
     * Deliberately absent: a create-comment method. The captured comment body
     * reads "This comment proves the native create and list contract", so a
     * create route plainly exists server-side — but no create request or
     * response appears anywhere in the capture. Writing one from the shape of
     * the list item would be inventing a request body, and a rejected POST
     * loses whatever the user typed.
     */
    @GET("v1/posts/{postId}/comments")
    suspend fun getComments(
        @Path("postId") postId: String,
        @Query("limit") limit: Int,
    ): ApiEnvelope<List<CommentDto>>
}

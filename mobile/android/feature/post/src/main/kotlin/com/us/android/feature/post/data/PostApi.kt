package com.us.android.feature.post.data

import com.us.android.core.network.ApiEnvelope
import com.us.android.feature.post.data.dto.BookmarkStatusDto
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

/**
 * Post and interaction endpoints.
 *
 * A Retrofit interface, not a client: created from the app-wide `Retrofit`, so
 * it inherits the base URL, auth, single-flight refresh, tracing and error
 * mapping. Every path here was exercised live on 2026-08-16 through the
 * gateway; see prompt/android-api-contracts.md §2.
 *
 * Nothing here is annotated `@Retryable`. Retry is opt-in for non-GET methods,
 * and none of these writes honours an idempotency key — see the bookmark note
 * below for what an automatic retry would actually do.
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
     * Bookmark TOGGLE. There is no separate add and remove.
     *
     * Two consequences the caller must respect:
     *
     *  1. The response's `bookmarked` value is authoritative. Do not assume the
     *     state flipped the way the tap intended.
     *  2. This must never be retried automatically, and must never sit in an
     *     offline queue. A replayed toggle lands in the opposite state, so a
     *     retry silently un-bookmarks what the user saved.
     *
     * `DELETE /v1/posts/:id/bookmark` exists in the router but is broken —
     * it returns 500 `relation "bookmarks" does not exist`. It is deliberately
     * not declared here; a second POST is the only working reversal.
     */
    @POST("v1/posts/{postId}/bookmark")
    suspend fun toggleBookmark(
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

    // Deliberately absent: GET /v1/posts/:postId/comments.
    //
    // It is reachable and returns 200, but the capture only ever observed
    // {"data":[]}. The comment item shape and its cursor have never been seen,
    // so a comment list here would be invented rather than implemented. The
    // count from the post payload is shown instead.
}

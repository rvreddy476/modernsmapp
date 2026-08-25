package com.us.android.feature.feed.data

import com.us.android.core.network.ApiEnvelope
import com.us.android.feature.feed.data.dto.FeedDeltaDto
import com.us.android.feature.feed.data.dto.FeedItemDto
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.POST
import retrofit2.http.Path
import retrofit2.http.Query

/**
 * Feed endpoints.
 *
 * A Retrofit interface created from the app-wide `Retrofit`; no client, no
 * base URL, no interceptor of its own. All five surfaces require a valid
 * access JWT — an unauthenticated call returns
 * `401 UNAUTHORIZED / "Invalid user ID"`.
 *
 * Verified against the 2026-08-17 repair capture with a seeded stack, which is
 * the first time these returned non-empty pages.
 */
interface FeedApi {

    /**
     * One request serves home, reels, videos and watch.
     *
     * `@Path` rather than four methods because the four differ only in the
     * path segment and in whether the response carries a cursor — and that
     * difference is enforced by [com.us.android.core.model.FeedSurface], where
     * it is visible, rather than by which function a caller happened to pick.
     *
     * [cursor] is `meta.next_cursor` from the previous page: an RFC3339
     * timestamp on home. It is passed back opaquely and never parsed or
     * constructed.
     */
    @GET("v1/feed/{surface}")
    suspend fun getFeed(
        @Path("surface") surface: String,
        @Query("limit") limit: Int,
        @Query("cursor") cursor: String? = null,
    ): ApiEnvelope<List<FeedItemDto>>

    /**
     * How many new items exist since [anchor].
     *
     * Both query parameters are required; omitting `feed_type` returns
     * `INVALID_REQUEST / "feed_type is required"`. This powers a "new posts"
     * pill, not a page load.
     */
    @GET("v1/feed/delta")
    suspend fun getDelta(
        @Query("feed_type") feedType: String,
        @Query("anchor") anchor: String,
        @Query("limit") limit: Int,
    ): ApiEnvelope<FeedDeltaDto>

    /**
     * Casts one poll vote. The server enforces poll-open and single-vote
     * rules; the client's optimistic flip is corrected on the next hydration.
     */
    @POST("v1/posts/{postId}/poll/vote")
    suspend fun votePoll(
        @Path("postId") postId: String,
        @Body body: PollVoteRequest,
    ): ApiEnvelope<Map<String, Boolean>>
}

@Serializable
data class PollVoteRequest(@SerialName("option_id") val optionId: String)

package com.us.android.feature.feed.data

import com.us.android.core.network.ApiEnvelope
import com.us.android.feature.feed.data.dto.FeedDeltaDto
import com.us.android.feature.feed.data.dto.FeedItemDto
import com.us.android.feature.feed.data.dto.TrendingHashtagsDto
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
     *
     * [followingOnly] and [circleOnly] are home's two narrowings
     * (feed-service handler.go reads `following_only` / `circle_only` as the
     * literal string `true`). Null OMITS the parameter — Retrofit drops a null
     * query — which the server reads as "the whole timeline". They are never
     * sent as `false`, so the request for the plain feed is byte-identical to
     * what it was before the tabs existed.
     */
    @GET("v1/feed/{surface}")
    suspend fun getFeed(
        @Path("surface") surface: String,
        @Query("limit") limit: Int,
        @Query("cursor") cursor: String? = null,
        @Query("following_only") followingOnly: Boolean? = null,
        @Query("circle_only") circleOnly: Boolean? = null,
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

    /**
     * Today's most-used hashtags — post-service `GetTrendingHashtagsFeed`.
     *
     * Not a list at the top level: the body is `{"data":{"hashtags":[…]}}`,
     * verified on the dev gateway on 2026-09-04 (an empty day answers
     * `{"data":{"hashtags":[]}}` with 200, not an absent key). [limit] is
     * clamped to 30 server-side.
     */
    @GET("v1/hashtags/trending")
    suspend fun getTrendingHashtags(
        @Query("limit") limit: Int,
    ): ApiEnvelope<TrendingHashtagsDto>

    /**
     * The posts carrying one tag — post-service `GetPostsByHashtag`.
     *
     * The rows are post-service's `PostDetail`, not feed-service's hydrated
     * item: the same field names for everything a card renders, but NO
     * embedded `author` and media as bare `{media_id, kind, position, alt_*}`
     * references with no delivery URLs. [FeedItemDto] decodes it because every
     * field defaults; the missing author and delivery are filled in by
     * [HashtagPostHydrator] before a row reaches the card.
     *
     * The server strips a leading `#` from [tag] itself. `meta.next_cursor`
     * is present only when a further page exists, exactly like home.
     */
    @GET("v1/hashtags/{tag}/posts")
    suspend fun getPostsByHashtag(
        @Path("tag") tag: String,
        @Query("limit") limit: Int,
        @Query("cursor") cursor: String? = null,
        @Query("sort") sort: String = HASHTAG_SORT_RECENT,
    ): ApiEnvelope<List<FeedItemDto>>

    /**
     * One post by id — post-service `GetPost`, the same `PostDetail` row the
     * hashtag list returns, so [FeedItemDto] decodes it and [HashtagPostHydrator]
     * fills in the author and the media delivery.
     *
     * Used for exactly one thing: putting a reel the viewer just posted at
     * the top of Reels the moment the server has created it, without waiting
     * for the ranked feed to carry it. A processing flick is visible to its
     * author here and nowhere else yet.
     */
    @GET("v1/posts/{postId}")
    suspend fun getPost(@Path("postId") postId: String): ApiEnvelope<FeedItemDto>

    /**
     * "Interested" / "Not interested" from the post "more" sheet —
     * feed-service `PostFeedback` (2026-09-04).
     *
     * Latest answer per (viewer, post) wins, so sending `interested` after a
     * `not_interested` is the undo. A `not_interested` drops the post from
     * every surface on the next fetch; the client removes it at once and does
     * not wait. Distinct from `/v1/feed/signal` (a ranking impression) and
     * from post-service's `/v1/feedback` (product notes).
     */
    @POST("v1/feed/feedback")
    suspend fun feedback(@Body body: FeedFeedbackRequest): ApiEnvelope<FeedFeedbackDto>

    companion object {
        /** The server's default; `top` is the other accepted value. */
        const val HASHTAG_SORT_RECENT = "recent"

        const val FEEDBACK_INTERESTED = "interested"
        const val FEEDBACK_NOT_INTERESTED = "not_interested"
    }
}

@Serializable
data class FeedFeedbackRequest(
    @SerialName("post_id") val postId: String,
    /** [FeedApi.FEEDBACK_INTERESTED] or [FeedApi.FEEDBACK_NOT_INTERESTED]. */
    val signal: String,
)

/** The stored row, echoed back. Nothing here is rendered; a 2xx is the signal. */
@Serializable
data class FeedFeedbackDto(
    @SerialName("post_id") val postId: String = "",
    val signal: String = "",
)

@Serializable
data class PollVoteRequest(@SerialName("option_id") val optionId: String)

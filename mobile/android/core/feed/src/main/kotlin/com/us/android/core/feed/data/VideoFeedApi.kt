package com.us.android.core.feed.data

import com.us.android.core.feed.data.dto.FeedItemDto
import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.GET
import retrofit2.http.Path
import retrofit2.http.Query

/**
 * The endpoints Tube's home, Subscriptions and You pages read (Tube
 * redesign, 2026-09-05). Its own interface rather than five more methods on
 * [FeedApi]: every fake `FeedApi` in the tests would otherwise grow members
 * that no feed screen calls, and the two are created from the same
 * app-wide Retrofit either way.
 */
interface VideoFeedApi {

    /**
     * `/v1/feed/videos` and `/v1/feed/watch`, with the two narrowings Tube
     * uses: `following_only=true` for Subscriptions and the Following chip,
     * `category=<slug>` for a category chip (feed-service, 2026-09-05: the
     * filter is applied after hydration and walks further windows to fill
     * the page). Both are omitted — null — for the plain surface, so the
     * request for "All" is byte-identical to what it was before the chips.
     */
    @GET("v1/feed/{surface}")
    suspend fun getFeed(
        @Path("surface") surface: String,
        @Query("limit") limit: Int,
        @Query("cursor") cursor: String? = null,
        @Query("following_only") followingOnly: Boolean? = null,
        @Query("category") category: String? = null,
    ): ApiEnvelope<List<FeedItemDto>>

    /**
     * The viewer's unfinished videos, most recently watched first —
     * post-service `GetContinueWatching`. Rows are watch-progress records
     * keyed by post id, each carrying its bare `PostDetail` as `post` since
     * 2026-09-05; a row from a server that predates the embed has none, and
     * its post is fetched by id.
     */
    @GET("v1/videos/continue-watching")
    suspend fun continueWatching(@Query("limit") limit: Int): ApiEnvelope<List<ContinueWatchingDto>>

    /**
     * One author's posts, newest first — post-service `GetPostsByAuthor`.
     * [type] is the `content_type` filter (`long_video` for Tube); the rows
     * are bare `PostDetail`, hydrated the way a hashtag page is.
     */
    @GET("v1/posts/by-author/{authorId}")
    suspend fun postsByAuthor(
        @Path("authorId") authorId: String,
        @Query("type") type: String,
        @Query("limit") limit: Int,
        @Query("cursor") cursor: String? = null,
    ): ApiEnvelope<List<FeedItemDto>>

    /** The viewer's bookmarks, every kind of post — post-service `GetBookmarks`. Bare rows again. */
    @GET("v1/posts/bookmarks")
    suspend fun bookmarks(
        @Query("limit") limit: Int,
        @Query("cursor") cursor: String? = null,
    ): ApiEnvelope<List<FeedItemDto>>

    /** The category taxonomy — `{"data":[{"id":"comedy","label":"Comedy"},…]}`. */
    @GET("v1/posts/categories")
    suspend fun categories(): ApiEnvelope<List<FeedCategoryDto>>

    /**
     * The viewer's posts that are scheduled and not yet live (2026-09-05),
     * soonest first — post-service `GET /v1/posts/me/scheduled`. Bare
     * `PostDetail` rows with `publish_at` and `is_scheduled`, hydrated like
     * any other bare row. Only the author can read them.
     */
    @GET("v1/posts/me/scheduled")
    suspend fun scheduled(
        @Query("limit") limit: Int,
        @Query("cursor") cursor: String? = null,
    ): ApiEnvelope<List<FeedItemDto>>
}

/** One continue-watching row: where the viewer left `post_id`, and the post itself when the server embeds it. */
@Serializable
data class ContinueWatchingDto(
    @SerialName("post_id") val postId: String = "",
    @SerialName("position_ms") val positionMs: Long = 0L,
    @SerialName("duration_ms") val durationMs: Long = 0L,
    val completed: Boolean = false,
    @SerialName("updated_at") val updatedAt: String = "",
    /** The bare `PostDetail` behind the row — hydrated client-side like any post-service row. Null before the embed. */
    val post: FeedItemDto? = null,
)

@Serializable
data class FeedCategoryDto(
    val id: String = "",
    val label: String = "",
)

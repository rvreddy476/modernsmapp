package com.us.android.core.feed.data

import com.us.android.core.network.ApiEnvelope
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.Path
import retrofit2.http.Query

/**
 * Tube's History (web parity, 2026-10-22): post-service's watch record, the
 * three routes the web's Recent screen calls (`usePosttubeExtras`,
 * `RecentScreen.tsx`), path for path.
 *
 * Its own interface for the reason [VideoLibraryApi] is one: the fakes of
 * [VideoFeedApi] would otherwise grow members no test of them calls.
 */
interface WatchHistoryApi {

    /**
     * `GET /v1/videos/history?limit&cursor` (`GetWatchHistory`): every
     * progress row of the viewer, finished or not, most recent first. Each
     * row is the continue-watching shape (progress fields plus the hydrated
     * `post`); `meta.next_cursor` is absent on the last page. The server
     * defaults to 20 and clamps at 100.
     */
    @GET("v1/videos/history")
    suspend fun history(
        @Query("limit") limit: Int,
        @Query("cursor") cursor: String?,
    ): ApiEnvelope<List<ContinueWatchingDto>>

    /** `DELETE /v1/videos/:videoId/progress`: one row out of History. 204, no body. */
    @DELETE("v1/videos/{postId}/progress")
    suspend fun remove(@Path("postId") postId: String)

    /** `DELETE /v1/videos/history`: every row, in one call. 204 either way, an empty history included. */
    @DELETE("v1/videos/history")
    suspend fun clear()
}

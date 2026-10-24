package com.us.android.core.feed.data

import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.json.JsonElement
import retrofit2.http.GET
import retrofit2.http.Path
import retrofit2.http.Query

/**
 * Trending sounds (2026-10-24), media-service, signed in like every other
 * sound route. Both answers are read as raw JSON and decoded by
 * [decodeTrendingSounds] / [trendingMarkOfSound], which drop what they
 * cannot read instead of failing the whole answer.
 *
 * Its own interface rather than two more methods on [SoundsApi], so the
 * fakes of that one (every sound test has one) stay as they are.
 */
interface TrendingSoundsApi {

    /**
     * The stored ranking: `data` is `[{rank, sound, uses_7d, uses_prev_7d,
     * rising}]`, `meta` carries `next_cursor` and `updated_at`. [filter] is
     * `all` or `rising`; [cursor] is the page before's `next_cursor`,
     * replayed verbatim, null on the first. The server's default limit is 20
     * and its ceiling 50.
     */
    @GET("v1/audio/trending")
    suspend fun trending(
        @Query("filter") filter: String,
        @Query("limit") limit: Int,
        @Query("cursor") cursor: String? = null,
    ): ApiEnvelope<JsonElement>

    /**
     * The sound's own row — the sound page's read on the web — for its
     * `trending` block (`{rank, rising} | null`). The page's sound itself
     * still comes from the by-sound page.
     */
    @GET("v1/audio/{soundId}")
    suspend fun sound(@Path("soundId") soundId: String): ApiEnvelope<JsonElement>
}

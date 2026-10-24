package com.us.android.core.feed.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ApiConfig
import com.us.android.core.network.ApiMeta
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Trending sounds (2026-10-24): a page of the ranking, and one sound's place
 * on it. The preview's bytes are the player's (`v1/audio/{id}/serve`), not
 * this repository's.
 */
@Singleton
class TrendingSoundsRepository @Inject constructor(
    private val api: TrendingSoundsApi,
    private val errorMapper: ErrorMapper,
    private val config: ApiConfig,
) {

    /** One page of [filter]; [cursor] is the page before's [TrendingSoundsPage.nextCursor], null for the first. */
    suspend fun page(
        filter: TrendingFilter,
        cursor: String? = null,
        limit: Int = PAGE_SIZE,
    ): AppResult<TrendingSoundsPage> {
        // The cursor and the time live in the envelope's `meta`, beside `data`.
        var meta: ApiMeta? = null
        val result = apiCall(errorMapper) {
            api.trending(filter.wire, limit.coerceIn(1, MAX_PAGE_SIZE), cursor).also { meta = it.meta }
        }
        return when (result) {
            is AppResult.Failure -> result
            is AppResult.Success ->
                AppResult.Success(trendingSoundsPage(result.data, meta?.nextCursor, meta?.updatedAt).withCovers())
        }
    }

    /**
     * Where [soundId] stands on the list, or null when it is not on it — or
     * when the read failed: a badge is a nicety, and its absence is never an
     * error the page says.
     */
    suspend fun mark(soundId: String): TrendingMark? {
        if (soundId.isBlank()) return null
        return when (val result = apiCall(errorMapper) { api.sound(soundId) }) {
            is AppResult.Failure -> null
            is AppResult.Success -> trendingMarkOfSound(result.data)
        }
    }

    /** Every cover on the API origin, or none: the row never loads a path it cannot reach. */
    private fun TrendingSoundsPage.withCovers() =
        copy(sounds = sounds.map { it.copy(coverUrl = resolveSoundCover(it.coverUrl, config.baseUrl)) })

    companion object {
        /** The server's default, and the shelf's size. */
        const val PAGE_SIZE = 20

        /** The server's ceiling. */
        const val MAX_PAGE_SIZE = 50
    }
}

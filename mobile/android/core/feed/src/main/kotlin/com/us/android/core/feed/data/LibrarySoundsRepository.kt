package com.us.android.core.feed.data

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.media.ChosenSound
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The curated sound library (2026-10-15): a page of it, and "Use". Both on
 * the app-wide Retrofit through [SoundsApi]; the preview's bytes are read by
 * the player, not here.
 */
@Singleton
class LibrarySoundsRepository @Inject constructor(
    private val api: SoundsApi,
    private val errorMapper: ErrorMapper,
) {

    /** One page, most used first; [genre] blank is every genre. */
    suspend fun page(genre: String, offset: Int, limit: Int = PAGE_SIZE): AppResult<LibrarySoundsPage> {
        val start = offset.coerceAtLeast(0)
        val size = limit.coerceIn(1, MAX_PAGE_SIZE)
        return when (
            val result = apiCall(errorMapper) {
                api.audioLibrary(genre.trim().takeIf { it.isNotEmpty() }, size, start)
            }
        ) {
            is AppResult.Failure -> result
            is AppResult.Success -> AppResult.Success(result.data.toPage(size, start))
        }
    }

    /**
     * "Use" [sound]: the sound a reel is made with, by its `audio_tracks` id.
     * A refusal is a failure the caller reads with [librarySoundUseFailure];
     * an answer that names no sound is [AppError.Malformed].
     */
    suspend fun use(sound: LibrarySound): AppResult<ChosenSound> =
        when (val result = apiCall(errorMapper) { api.useLibrarySound(sound.id) }) {
            is AppResult.Failure -> result
            is AppResult.Success -> result.data.toChosenSound(sound)
                ?.let { AppResult.Success(it) }
                ?: AppResult.Failure(AppError.Malformed(detail = "the library sound came back without a track id"))
        }

    companion object {
        /** The server's default and the web hub's page. */
        const val PAGE_SIZE = 20

        /** What the web asks for at most. */
        const val MAX_PAGE_SIZE = 50
    }
}

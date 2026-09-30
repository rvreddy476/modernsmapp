package com.us.android.core.feed.data

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.model.ReelSound
import com.us.android.core.model.toReelSound
import com.us.android.core.network.ApiMeta
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Original sounds (2026-09-30): "use this sound", the reels that play one,
 * and the sound's own row.
 *
 * The rows of a by-sound page are post-service's bare posts, so each page
 * goes through the same [FeedItemHydrator] a hashtag page does to pick up the
 * author and the media delivery a tile and the reel player need.
 */
@Singleton
class SoundsRepository @Inject constructor(
    private val api: SoundsApi,
    private val errorMapper: ErrorMapper,
    private val hydrator: FeedItemHydrator,
) {

    /**
     * The sound [postId] plays, made from the reel's own audio on first use.
     * A refusal is a failure whose code the caller reads with
     * [soundRefusalCode]; an answer without a usable sound is malformed, not
     * a sound with an empty id.
     */
    suspend fun useSound(postId: String): AppResult<ReelSound> =
        when (val result = apiCall(errorMapper) { api.useSound(postId) }) {
            is AppResult.Failure -> result
            is AppResult.Success -> result.data.sound?.toWire().toReelSound()
                ?.let { AppResult.Success(it) }
                ?: AppResult.Failure(AppError.Malformed(detail = "the sound came back without an id"))
        }

    /**
     * One page of the reels that play [soundId], newest first. [cursor] is
     * the page before's [SoundReelsPage.nextCursor], or null for the first
     * page. A sound that is gone or may not be heard is [AppError.NotFound].
     */
    suspend fun reels(soundId: String, cursor: String? = null): AppResult<SoundReelsPage> {
        // The cursor lives in the envelope's `meta`, beside `data`, so it is
        // taken as the envelope goes by.
        var meta: ApiMeta? = null
        val result = apiCall(errorMapper) {
            api.reelsBySound(soundId, SOUND_REELS_PAGE_SIZE, cursor).also { meta = it.meta }
        }
        return when (result) {
            is AppResult.Failure -> result
            is AppResult.Success -> AppResult.Success(result.data.toPage(meta).hydrated())
        }
    }

    /**
     * The sound's own row, or [AppError.NotFound] when it is missing, not
     * this viewer's to hear, or not ready to be played — one answer for all
     * three, as the server gives for the first two.
     */
    suspend fun sound(soundId: String): AppResult<ReelSound> =
        when (val result = apiCall(errorMapper) { api.sound(soundId) }) {
            is AppResult.Failure -> result
            is AppResult.Success -> result.data.toReelSound()
                ?.let { AppResult.Success(it) }
                ?: AppResult.Failure(AppError.NotFound())
        }

    /** The origin and the items through the hydrator in ONE call, so an author shared by both is read once. */
    private suspend fun SoundReelsPage.hydrated(): SoundReelsPage {
        val rows = hydrator.hydrate(listOfNotNull(origin) + items)
        val byId = rows.associateBy { it.id }
        return copy(
            origin = origin?.let { byId[it.id] ?: it },
            items = items.map { byId[it.id] ?: it },
        )
    }

    companion object {
        /** The server's default, and the web's page size: a grid of three fills eight rows. */
        const val SOUND_REELS_PAGE_SIZE = 24
    }
}

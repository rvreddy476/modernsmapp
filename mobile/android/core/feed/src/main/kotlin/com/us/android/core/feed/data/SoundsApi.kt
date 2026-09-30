package com.us.android.core.feed.data

import com.us.android.core.feed.data.dto.FeedItemDto
import com.us.android.core.feed.data.dto.FeedSoundDto
import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.GET
import retrofit2.http.POST
import retrofit2.http.Path
import retrofit2.http.Query

/**
 * Original sounds (2026-09-30): a reel can play the audio of another
 * creator's public reel. Three reads and one write, all on the app-wide
 * Retrofit.
 *
 * A sound is answered only to a viewer who may watch its SOURCE video, and a
 * refusal is byte for byte the missing answer: a 404 here means "gone or not
 * yours to hear", and the screens say one thing for both.
 *
 * The sound's BYTES are not here. They are `GET v1/audio/{id}/serve`, read by
 * the player through the media data-source chain — see
 * `com.us.android.core.media.sound.soundServeUrl`.
 */
interface SoundsApi {

    /**
     * "Use this sound" — post-service `UseSound` (contract 2.2). Answers the
     * sound a reel plays, creating it from the reel's own audio on first use.
     * No body. Refusals carry a code: 403 `SOUND_REUSE_NOT_ALLOWED`, 404
     * `NOT_FOUND`, 422 `NOT_A_REEL` | `NOT_READY` | `TOO_LONG` | `NO_AUDIO`,
     * 429 `RATE_LIMITED`, 503 `SOUND_UNAVAILABLE`.
     */
    @POST("v1/posts/{postId}/sound")
    suspend fun useSound(@Path("postId") postId: String): ApiEnvelope<UseSoundDto>

    /**
     * The reels that play a sound, newest first — post-service
     * `GetPostsBySound` (contract 2.3). [cursor] is `meta.next_cursor` of the
     * page before, replayed verbatim; null omits it, and the first page is
     * the only one that carries `origin`. The server's default limit is 24
     * and its ceiling 50.
     */
    @GET("v1/posts/by-sound/{soundId}")
    suspend fun reelsBySound(
        @Path("soundId") soundId: String,
        @Query("limit") limit: Int,
        @Query("cursor") cursor: String? = null,
    ): ApiEnvelope<SoundReelsDto>

    /** The sound's own row — media-service (contract 1.3, shape 1.4). */
    @GET("v1/audio/{soundId}")
    suspend fun sound(@Path("soundId") soundId: String): ApiEnvelope<SoundRowDto>
}

/** `POST v1/posts/{id}/sound` → `{sound}`. */
@Serializable
data class UseSoundDto(
    val sound: FeedSoundDto? = null,
)

/**
 * `GET v1/posts/by-sound/{id}` → `{sound, origin, items}`. The rows are
 * post-service's bare `Post`, the same shape a hashtag page returns: no
 * embedded author and no media delivery, both filled in by the hydrator.
 */
@Serializable
data class SoundReelsDto(
    val sound: FeedSoundDto? = null,
    /** The reel the sound was taken from: first page only, and only when this viewer may read it. */
    val origin: FeedItemDto? = null,
    val items: List<FeedItemDto> = emptyList(),
)

/**
 * media-service's sound row, exactly the keys its handler writes. The
 * storage keys (`audio_key`, `waveform_key`) are not on the wire and are not
 * here. `source_reel_id` is kept beside `source_post_id` with the same value.
 */
@Serializable
data class SoundRowDto(
    val id: String = "",
    val title: String = "",
    val artist: String = "",
    @SerialName("duration_ms") val durationMs: Long = 0L,
    @SerialName("sample_rate") val sampleRate: Int = 0,
    /** `ready` (or `active`) when it can be played; blank on older rows, read as ready. */
    val status: String = "",
    @SerialName("is_original") val isOriginal: Boolean = false,
    @SerialName("license_type") val licenseType: String = "",
    @SerialName("usage_count") val usageCount: Long = 0L,
    @SerialName("source_media_id") val sourceMediaId: String? = null,
    @SerialName("source_post_id") val sourcePostId: String? = null,
    @SerialName("source_reel_id") val sourceReelId: String? = null,
    @SerialName("creator_user_id") val creatorUserId: String? = null,
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("updated_at") val updatedAt: String = "",
)

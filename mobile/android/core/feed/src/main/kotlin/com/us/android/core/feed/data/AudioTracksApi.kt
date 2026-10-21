package com.us.android.core.feed.data

import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import okhttp3.MultipartBody
import okhttp3.RequestBody
import retrofit2.http.Body
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.Multipart
import retrofit2.http.POST
import retrofit2.http.Part
import retrofit2.http.Path

/**
 * media-service's alternate audio tracks (2026-09-27; read from
 * `internal/http/audio_tracks_handler.go`):
 *
 *     GET    /v1/media/:id/audio-tracks           any viewer the serve gate admits
 *                                                 → {"data":{"tracks":[…]}}, the original first
 *     POST   /v1/media/:id/audio-tracks           owner; multipart file + language [+ label] → 201, the track
 *     POST   /v1/media/:id/audio-tracks/generate  owner; {language, source_language?} → 202, the track
 *     DELETE /v1/media/:id/audio-tracks/:trackId  owner → 204
 *
 * Refusals: 403 FORBIDDEN (not the owner), 404 NOT_FOUND, 409
 * MEDIA_NOT_READY, 413 PAYLOAD_TOO_LARGE (over 50 MB), 422
 * AUDIO_DURATION_MISMATCH (the length must match within max(3 s, 5%)), 422
 * AUDIO_TRACK_LIMIT (ten), 400 INVALID_AUDIO_TRACK, and 503
 * DUBBING_UNAVAILABLE for a generate while AI dubbing is switched off.
 *
 * Its own interface for the reason [VideoFeedApi] is one: fakes of the
 * other APIs do not grow members no screen of theirs calls.
 */
interface AudioTracksApi {

    @GET("v1/media/{mediaId}/audio-tracks")
    suspend fun list(@Path("mediaId") mediaId: String): ApiEnvelope<AudioTracksDto>

    /** The form fields are the handler's own: `file`, `language`, `label` (optional). */
    @Multipart
    @POST("v1/media/{mediaId}/audio-tracks")
    suspend fun upload(
        @Path("mediaId") mediaId: String,
        @Part file: MultipartBody.Part,
        @Part("language") language: RequestBody,
        @Part("label") label: RequestBody?,
    ): ApiEnvelope<AudioTrackDto>

    @POST("v1/media/{mediaId}/audio-tracks/generate")
    suspend fun generate(
        @Path("mediaId") mediaId: String,
        @Body body: GenerateAudioTrackRequest,
    ): ApiEnvelope<AudioTrackDto>

    /** 204 with no body: a `Unit` return so Retrofit converts nothing. */
    @DELETE("v1/media/{mediaId}/audio-tracks/{trackId}")
    suspend fun delete(@Path("mediaId") mediaId: String, @Path("trackId") trackId: String)
}

/** The list's `data`: `{"tracks":[…]}`. */
@Serializable
data class AudioTracksDto(val tracks: List<AudioTrackDto> = emptyList())

/** One track — media-service's `AudioTrackView`, field for field. */
@Serializable
data class AudioTrackDto(
    val id: String = "",
    val language: String = "",
    val label: String = "",
    /** original | uploaded | generated */
    val source: String = "",
    /** pending | processing | ready | failed */
    val status: String = "",
    @SerialName("playback_url") val playbackUrl: String? = null,
    val rungs: List<String> = emptyList(),
    val error: String? = null,
    @SerialName("created_at") val createdAt: String? = null,
)

/** The generate body. `source_language` is dropped when null (the app's JSON omits nulls). */
@Serializable
data class GenerateAudioTrackRequest(
    val language: String,
    @SerialName("source_language") val sourceLanguage: String? = null,
)

fun AudioTrackDto.toAudioTrack(): AudioTrack? {
    if (id.isBlank()) return null
    return AudioTrack(
        id = id,
        language = language.lowercase(),
        label = label,
        source = AudioTrackSource.fromWire(source),
        status = AudioTrackStatus.fromWire(status),
        playbackUrl = playbackUrl?.takeIf { it.isNotBlank() },
        error = error?.takeIf { it.isNotBlank() },
    )
}

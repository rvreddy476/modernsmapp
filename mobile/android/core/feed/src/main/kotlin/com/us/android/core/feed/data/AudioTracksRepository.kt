package com.us.android.core.feed.data

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.core.network.noContentApiCall
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.MultipartBody
import okhttp3.RequestBody
import okhttp3.RequestBody.Companion.toRequestBody
import java.util.concurrent.ConcurrentHashMap
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The alternate audio tracks of every video this process has asked about,
 * keyed by MEDIA id (2026-10-21). One cache for the viewer's picker on a reel
 * and a long video, and for the creator's sheet: an upload or a delete in
 * the sheet is what the More menu shows next.
 */
@Singleton
class AudioTracksRepository @Inject constructor(
    private val api: AudioTracksApi,
    private val errorMapper: ErrorMapper,
) {

    private val _tracks = MutableStateFlow<Map<String, List<AudioTrack>>>(emptyMap())

    /** Media id → its tracks, the original first; absent until read. */
    val tracks: StateFlow<Map<String, List<AudioTrack>>> = _tracks.asStateFlow()

    /** Reads in flight, so a reel settling twice asks once. */
    private val reading = ConcurrentHashMap.newKeySet<String>()

    /**
     * Reads [mediaId]'s tracks unless they are already known. A video the
     * viewer may not have, or that does not exist, is remembered as having
     * the original alone, so it is not asked again on every swipe; any other
     * failure is left unknown, to be asked again next time.
     */
    suspend fun ensure(mediaId: String) {
        if (mediaId.isBlank() || _tracks.value.containsKey(mediaId)) return
        if (!reading.add(mediaId)) return
        try {
            when (val result = refresh(mediaId)) {
                is AppResult.Success -> Unit
                is AppResult.Failure -> when (result.error) {
                    is AppError.NotFound, is AppError.Forbidden -> remember(mediaId, emptyList())
                    else -> Unit
                }
            }
        } finally {
            reading.remove(mediaId)
        }
    }

    /** Reads [mediaId]'s tracks again, whatever is known: the creator's sheet polls through this. */
    suspend fun refresh(mediaId: String): AppResult<List<AudioTrack>> =
        when (val result = apiCall(errorMapper) { api.list(mediaId) }) {
            is AppResult.Failure -> result
            is AppResult.Success -> {
                val tracks = result.data.tracks.mapNotNull { it.toAudioTrack() }
                remember(mediaId, tracks)
                AppResult.Success(tracks)
            }
        }

    /** The creator's upload: one audio file for one language. The list is read again after it lands. */
    suspend fun upload(mediaId: String, file: AudioUploadFile, language: String, label: String): AppResult<AudioTrack> {
        val parts = audioUploadParts(file, language, label)
        val result = apiCall(errorMapper) { api.upload(mediaId, parts.file, parts.language, parts.label) }
        return afterWrite(mediaId, result)
    }

    /** "Generate": an AI dub into [language]. 503 DUBBING_UNAVAILABLE while the server has it switched off. */
    suspend fun generate(mediaId: String, language: String): AppResult<AudioTrack> {
        val result = apiCall(errorMapper) { api.generate(mediaId, GenerateAudioTrackRequest(language = language)) }
        return afterWrite(mediaId, result)
    }

    /** Removes one track. The cache drops it at once on success; a 404 is a track already gone. */
    suspend fun delete(mediaId: String, trackId: String): AppResult<Unit> {
        val result = noContentApiCall(errorMapper) { api.delete(mediaId, trackId) }
        val gone = result is AppResult.Success || (result as? AppResult.Failure)?.error is AppError.NotFound
        if (!gone) return result
        _tracks.update { all ->
            val list = all[mediaId] ?: return@update all
            all + (mediaId to list.filterNot { it.id == trackId })
        }
        return AppResult.Success(Unit)
    }

    private suspend fun afterWrite(mediaId: String, result: AppResult<AudioTrackDto>): AppResult<AudioTrack> =
        when (result) {
            is AppResult.Failure -> result
            is AppResult.Success -> {
                refresh(mediaId)
                result.data.toAudioTrack()?.let { AppResult.Success(it) }
                    ?: AppResult.Failure(AppError.Malformed(detail = "the track came back without an id"))
            }
        }

    private fun remember(mediaId: String, tracks: List<AudioTrack>) {
        _tracks.update { it + (mediaId to tracks) }
    }
}

/** A picked audio file, ready to send: its name and type, and a body that reads it as it goes. */
class AudioUploadFile(val name: String, val mimeType: String, val body: RequestBody)

/** The three multipart pieces of an upload, as the handler reads them. */
class AudioUploadParts(val file: MultipartBody.Part, val language: RequestBody, val label: RequestBody?)

/** A blank label is left off, and the server names the track after its language. */
fun audioUploadParts(file: AudioUploadFile, language: String, label: String): AudioUploadParts {
    val text = "text/plain".toMediaType()
    return AudioUploadParts(
        file = MultipartBody.Part.createFormData(FILE_FIELD, file.name, file.body),
        language = language.toRequestBody(text),
        label = label.trim().takeIf { it.isNotEmpty() }?.toRequestBody(text),
    )
}

private const val FILE_FIELD = "file"

/** The server's code for a refusal, wherever the mapper put it. */
fun AppError.serverCode(): String? = when (this) {
    is AppError.Server -> code
    is AppError.Unknown -> code
    is AppError.Forbidden -> code
    else -> null
}

/** 503 DUBBING_UNAVAILABLE: AI dubbing is switched off on this server. Uploads still work. */
fun AppError.isDubbingUnavailable(): Boolean =
    serverCode() == "DUBBING_UNAVAILABLE" || (this is AppError.Server && statusCode == HTTP_UNAVAILABLE)

/** What the creator's sheet says when an upload, a generate or a delete is refused — the web's sentences. */
fun audioTrackRefusal(error: AppError, fallback: String = "Please try again."): String = when {
    error.isDubbingUnavailable() -> DUBBING_UNAVAILABLE_MESSAGE
    error is AppError.NoNetwork || error is AppError.Timeout -> "You're offline. Try again when you're connected."
    else -> when (error.serverCode()) {
        "AUDIO_DURATION_MISMATCH" -> "That audio is not the same length as the video."
        "AUDIO_TRACK_LIMIT" -> "A video can carry up to $MAX_AUDIO_TRACKS audio tracks."
        "MEDIA_NOT_READY" -> "The video is still processing. Try again in a moment."
        "PAYLOAD_TOO_LARGE" -> "That file is too large (50 MB max)."
        "INVALID_AUDIO_TRACK" -> "That file can't be used. Use m4a, mp3, wav, ogg or flac."
        "FORBIDDEN" -> "Only the video's owner can change its audio tracks."
        else -> fallback
    }
}

/** Said under Generate once the server has answered DUBBING_UNAVAILABLE. */
const val DUBBING_UNAVAILABLE_MESSAGE = "Generated dubs are not available on this server yet. Uploads still work."

private const val HTTP_UNAVAILABLE = 503

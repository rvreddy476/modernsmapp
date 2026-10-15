package com.us.android.core.media.sound

import com.us.android.core.media.Playback
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull

/**
 * Which sound to play beside a video, and where in it playback starts —
 * everything this module needs to know of a reel's added sound. `:core:media`
 * knows nothing about a reel or a post, so the feature hands it these three
 * facts rather than its domain type.
 */
data class SoundTrack(
    val id: String,
    /** Where in the sound playback starts. */
    val startMs: Long = 0L,
    /** What the server declared; 0 when it did not say, and the player then reads it from the file. */
    val durationMs: Long = 0L,
    /**
     * The sound's stored copy, when the reel was saved offline (2026-10-02).
     * It is then played from the device and the `serve` route is not asked;
     * null plays the sound from the network, as before.
     */
    val stored: Playback? = null,
)

/** The creator's two levels, 0..1 each: the reel's own audio, and the sound beside it. */
data class SoundMix(
    val originalVolume: Double = 1.0,
    val overlayVolume: Double = 1.0,
)

/**
 * Where a sound's bytes are: `{API base}/v1/audio/{id}/serve` (contract 1.2).
 *
 * The route is OURS, so the request carries the bearer token; it answers 307
 * to a signed storage link that lives five minutes, and the token must not
 * follow the redirect there — OkHttp drops `Authorization` when a redirect
 * leaves the host, which `SoundRedirectTest` pins. The address is asked for
 * again on every load, so there is no expiry to track here.
 *
 * The id is one path segment, encoded as one: an id cannot break out of its
 * place in the address. Null when the base URL is not a URL at all or the id
 * is blank — there is then no sound to ask for.
 */
fun soundServeUrl(baseUrl: String, soundId: String): String? {
    val id = soundId.trim()
    if (id.isEmpty()) return null
    val base = baseUrl.toHttpUrlOrNull() ?: return null
    return base.newBuilder()
        .addPathSegments(AUDIO_PATH)
        .addPathSegment(id)
        .addPathSegment(SERVE_SEGMENT)
        .build()
        .toString()
}

/**
 * Where a LIBRARY sound's preview is: `{API base}/v1/audio-library/{id}/serve`
 * (library sounds, 2026-10-15). A curated library row has no sound of its own
 * until someone uses it, so it cannot be asked for by a sound id; this route
 * answers the same 307 as [soundServeUrl], to the presigned audio or to the
 * row's own https address. Public, and the token is dropped on the redirect
 * exactly as it is there.
 *
 * Only for listening BEFORE "Use": the sound a post carries is always the
 * `audio_track_id` the use answers, played through [soundServeUrl].
 */
fun librarySoundServeUrl(baseUrl: String, libraryId: String): String? {
    val id = libraryId.trim()
    if (id.isEmpty()) return null
    val base = baseUrl.toHttpUrlOrNull() ?: return null
    return base.newBuilder()
        .addPathSegments(AUDIO_LIBRARY_PATH)
        .addPathSegment(id)
        .addPathSegment(SERVE_SEGMENT)
        .build()
        .toString()
}

private const val AUDIO_PATH = "v1/audio"
private const val AUDIO_LIBRARY_PATH = "v1/audio-library"
private const val SERVE_SEGMENT = "serve"

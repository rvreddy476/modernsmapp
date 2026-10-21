package com.us.android.core.feed.data

import com.us.android.core.ui.UsAudioTrackOption
import java.util.Locale

/*
 * Alternate audio tracks of a video (media-service, 2026-09-27; on Android
 * 2026-10-21, matching the web's `playback/audioTracks.ts`).
 *
 * The viewer keeps ONE preference, a language; every reel or long video that
 * carries a READY track in that language plays it, everything else plays the
 * original. A preference a video cannot satisfy falls back to the original,
 * never to some other language. Pure, so the rules are table tests.
 */

/** Where a track came from. The server's words; anything else reads as [UPLOADED]. */
enum class AudioTrackSource(val wire: String) {
    ORIGINAL("original"),
    UPLOADED("uploaded"),
    GENERATED("generated"),
    ;

    companion object {
        fun fromWire(value: String): AudioTrackSource =
            entries.firstOrNull { it.wire == value.lowercase() } ?: UPLOADED
    }
}

/** Where a track stands. A track the server named with some later word is [PENDING]: not playable yet. */
enum class AudioTrackStatus(val wire: String) {
    PENDING("pending"),
    PROCESSING("processing"),
    READY("ready"),
    FAILED("failed"),
    ;

    /** Still being made: the creator's sheet polls while any track is. */
    val working: Boolean get() = this == PENDING || this == PROCESSING

    companion object {
        fun fromWire(value: String): AudioTrackStatus =
            entries.firstOrNull { it.wire == value.lowercase() } ?: PENDING
    }
}

/**
 * One track as `GET /v1/media/:id/audio-tracks` lists it. The original is
 * the first row, id `original`, with no [playbackUrl]: it is what the video
 * already plays.
 */
data class AudioTrack(
    val id: String,
    /** A lowercase language tag (`hi`); the original's is `und`. */
    val language: String,
    val label: String,
    val source: AudioTrackSource,
    val status: AudioTrackStatus,
    /** Gateway-relative MP4 with this audio muxed in (`/v1/media/<id>/serve/dub_hi_720p`); ready tracks only. */
    val playbackUrl: String? = null,
    /** Why a failed track failed, in the server's words. */
    val error: String? = null,
) {
    val isOriginal: Boolean get() = source == AudioTrackSource.ORIGINAL

    /** A dub a viewer can switch to: ready, and with something to play. */
    val playable: Boolean get() = !isOriginal && status == AudioTrackStatus.READY && !playbackUrl.isNullOrBlank()
}

/** The original's id, on the wire and in the picker. */
const val ORIGINAL_AUDIO_TRACK_ID = "original"

/** What the original is called when the server sends no label. */
private const val ORIGINAL_LABEL = "Original"

/**
 * The picker's choices: the original first, then every PLAYABLE alternate.
 * A pending, processing or failed track is not offered; neither is a ready
 * one with nothing to play.
 */
fun audioTrackOptions(tracks: List<AudioTrack>): List<UsAudioTrackOption> {
    val original = tracks.firstOrNull { it.isOriginal }
    val originalLabel = original?.label?.takeIf { it.isNotBlank() } ?: ORIGINAL_LABEL
    val head = UsAudioTrackOption(ORIGINAL_AUDIO_TRACK_ID, originalLabel)
    return listOf(head) + tracks.filter { it.playable }.map { track ->
        UsAudioTrackOption(track.id, track.label.ifBlank { audioLanguageLabel(track.language) })
    }
}

/**
 * Which track this video should play for a viewer who prefers [language]
 * (null = the original): the ready alternate in that language, else null —
 * the original. The language match ignores case, as the web's does.
 */
fun pickAudioTrack(tracks: List<AudioTrack>, language: String?): AudioTrack? {
    val want = language?.takeIf { it.isNotBlank() }?.lowercase() ?: return null
    return tracks.firstOrNull { it.playable && it.language.lowercase() == want }
}

/** The language a picker choice stands for: null for the original, and for an id this list does not have. */
fun languageForChoice(tracks: List<AudioTrack>, trackId: String): String? {
    if (trackId == ORIGINAL_AUDIO_TRACK_ID) return null
    return tracks.firstOrNull { it.id == trackId }?.language?.lowercase()
}

/** The id the picker marks as playing for [language]: the matching ready dub's, else the original's. */
fun activeAudioTrackId(tracks: List<AudioTrack>, language: String?): String =
    pickAudioTrack(tracks, language)?.id ?: ORIGINAL_AUDIO_TRACK_ID

/** One language a creator can pick for an upload or a generated dub. */
data class AudioLanguage(val tag: String, val label: String)

/** The web's list (`LANGUAGE_CHOICES`), in its order. */
val AUDIO_LANGUAGE_CHOICES: List<AudioLanguage> = listOf(
    AudioLanguage("en", "English"),
    AudioLanguage("hi", "Hindi"),
    AudioLanguage("te", "Telugu"),
    AudioLanguage("ta", "Tamil"),
    AudioLanguage("kn", "Kannada"),
    AudioLanguage("ml", "Malayalam"),
    AudioLanguage("mr", "Marathi"),
    AudioLanguage("bn", "Bengali"),
    AudioLanguage("gu", "Gujarati"),
    AudioLanguage("pa", "Punjabi"),
    AudioLanguage("ur", "Urdu"),
    AudioLanguage("es", "Spanish"),
    AudioLanguage("fr", "French"),
    AudioLanguage("de", "German"),
    AudioLanguage("pt", "Portuguese"),
    AudioLanguage("ar", "Arabic"),
    AudioLanguage("ja", "Japanese"),
    AudioLanguage("ko", "Korean"),
    AudioLanguage("zh", "Chinese"),
)

/** A tag's name: the list's, else the platform's English name, else the tag itself. */
fun audioLanguageLabel(tag: String): String {
    AUDIO_LANGUAGE_CHOICES.firstOrNull { it.tag == tag.lowercase() }?.let { return it.label }
    val name = Locale.forLanguageTag(tag).getDisplayLanguage(Locale.ENGLISH)
    return name.takeIf { it.isNotBlank() && !it.equals(tag, ignoreCase = true) } ?: tag
}

/** The most tracks a video can carry besides the original (media-service's limit). */
const val MAX_AUDIO_TRACKS = 10

package com.us.android.core.feed.data

import com.us.android.core.common.error.AppError
import com.us.android.core.common.time.formatDuration
import com.us.android.core.media.ChosenSound
import java.util.Locale

/*
 * Library sounds (2026-10-15): the curated sound library a creator can put
 * on a reel, beside the sounds taken from other reels. What a row is, what a
 * page is, and what a refused "Use" says. All pure, so the rules are pinned
 * by tests; the web's hub `hubApi.ts` (normalizeLibrarySound,
 * librarySoundUseFailure), rule for rule.
 *
 * The one rule that matters: a post NEVER carries a library id. "Use" asks
 * media-service for the `audio_tracks` id the row plays as, and that id goes
 * where an extracted sound's does — `SoundEntry`, then the reel's
 * `audio_track_id`.
 */

/** One curated library row, as the list shows it. */
data class LibrarySound(
    /** The LIBRARY id: for the preview and for "Use", never for a post. */
    val id: String,
    val title: String,
    val artist: String,
    val durationMs: Long,
    val genre: String,
    val mood: String,
    val usageCount: Long,
    val isTrending: Boolean,
    /** The sound this row was made into on its first use; null until then. */
    val audioTrackId: String?,
    /** False when the server has nothing to play for it: no preview button. */
    val playable: Boolean,
)

/** One page of the library. */
data class LibrarySoundsPage(
    val sounds: List<LibrarySound>,
    /** The offset of the next page, or null when this page was the last. */
    val nextOffset: Int?,
)

/** What the list calls a row with no title. */
const val UNTITLED_SOUND = "Untitled sound"

/** A wire row as a [LibrarySound], or null when it has no id or is inactive. */
fun LibraryTrackDto.toLibrarySound(): LibrarySound? {
    val libraryId = id.trim()
    if (libraryId.isEmpty() || !isActive) return null
    return LibrarySound(
        id = libraryId,
        title = title.trim().ifEmpty { UNTITLED_SOUND },
        artist = artist.trim(),
        durationMs = durationMs.coerceAtLeast(0L),
        genre = genre.orEmpty().trim(),
        mood = mood.orEmpty().trim(),
        usageCount = usageCount.coerceAtLeast(0L),
        isTrending = isTrending,
        audioTrackId = audioTrackId?.trim()?.takeIf { it.isNotEmpty() },
        playable = audioUrl.isNotBlank(),
    )
}

/**
 * `{tracks, total}` as a page. The next page starts after every row the
 * server sent — dropped ones included — and there is one only when this page
 * was full: `total` counts this page's rows, not the library's.
 */
fun LibraryTracksDto.toPage(limit: Int, offset: Int): LibrarySoundsPage = LibrarySoundsPage(
    sounds = tracks.mapNotNull { it.toLibrarySound() },
    nextOffset = (offset + tracks.size).takeIf { tracks.isNotEmpty() && tracks.size >= limit },
)

/**
 * "Use"'s answer as the sound the reel is made with: ALWAYS the
 * `audio_tracks` id (`audio_track_id`, else the returned sound's own id,
 * which is the same row), never [row]'s library id. What it is called comes
 * from the returned sound, else from the row the creator pressed. Null when
 * the answer names no sound — a malformed answer, not a sound with no id.
 */
fun LibrarySoundUseDto.toChosenSound(row: LibrarySound): ChosenSound? {
    val trackId = audioTrackId?.trim()?.takeIf { it.isNotEmpty() }
        ?: sound?.id?.trim()?.takeIf { it.isNotEmpty() }
        ?: return null
    return ChosenSound(
        id = trackId,
        title = sound?.title?.trim()?.takeIf { it.isNotEmpty() } ?: row.title,
        artist = sound?.artist?.trim()?.takeIf { it.isNotEmpty() } ?: row.artist,
        durationMs = sound?.durationMs?.takeIf { it > 0L } ?: row.durationMs,
    )
}

/** Every genre seen so far, once each, in order: the filter keeps its options once one is chosen. */
fun libraryGenres(known: List<String>, sounds: List<LibrarySound>): List<String> =
    (known + sounds.map { it.genre }.filter { it.isNotBlank() })
        .distinctBy { it.lowercase(Locale.ROOT) }
        .sortedBy { it.lowercase(Locale.ROOT) }

/** A genre as a pill says it: "lo-fi" → "Lo-fi". */
fun genreLabel(genre: String): String =
    genre.trim().replaceFirstChar { if (it.isLowerCase()) it.titlecase(Locale.ROOT) else it.toString() }

/** A row's second line: "Asha · 0:28 · Pop · Calm", whatever is known of it. */
fun librarySoundMeta(sound: LibrarySound): String = listOfNotNull(
    sound.artist.takeIf { it.isNotBlank() },
    sound.durationMs.takeIf { it > 0L }?.let(::formatDuration),
    sound.genre.takeIf { it.isNotBlank() }?.let(::genreLabel),
    sound.mood.takeIf { it.isNotBlank() }?.let(::genreLabel),
).joinToString(" · ")

/** "1 video", "1,200 videos"; nothing at zero. */
fun libraryUsageLabel(count: Long): String = when {
    count <= 0L -> ""
    count == 1L -> "1 video"
    else -> "${String.format(Locale.US, "%,d", count)} videos"
}

// ── a refused "Use" ─────────────────────────────────────────────────────

/** Why "Use" failed, by the server's code or status — never its message. */
enum class LibrarySoundUseFailure(val message: String) {
    /** 401: the session is over. The app's sign-in takes over once the session is cleared. */
    SIGNED_OUT("Please sign in again to use this sound."),

    /** 404 `NOT_FOUND`: no such row, inactive, or withdrawn. */
    GONE("This sound is no longer in the library."),

    /** 422 `NOT_STORED`: the row has no audio the service can deliver. */
    NOT_STORED("This sound has no audio to use. Choose another one."),

    /** 503 `SOUND_UNAVAILABLE`: the sound could not be prepared; worth a retry. */
    UNAVAILABLE("This sound couldn't be prepared. Try again in a moment."),

    /** No network, a timeout. */
    OFFLINE("You're offline. Check your connection and try again."),

    /** Anything else, a malformed answer included. */
    OTHER("That sound couldn't be used. Please try again."),
}

/** [LibrarySoundUseFailure] for an error as the repository answers it. */
fun AppError.librarySoundUseFailure(): LibrarySoundUseFailure = when (this) {
    is AppError.AuthFailed -> LibrarySoundUseFailure.SIGNED_OUT
    is AppError.NotFound -> LibrarySoundUseFailure.GONE
    is AppError.NoNetwork, is AppError.Timeout -> LibrarySoundUseFailure.OFFLINE
    is AppError.Server -> if (code == CODE_SOUND_UNAVAILABLE || statusCode == HTTP_UNAVAILABLE) {
        LibrarySoundUseFailure.UNAVAILABLE
    } else {
        LibrarySoundUseFailure.OTHER
    }
    is AppError.Unknown -> when {
        code == CODE_NOT_STORED || (code == null && statusCode == HTTP_UNPROCESSABLE) -> LibrarySoundUseFailure.NOT_STORED
        code == CODE_UNAUTHORIZED -> LibrarySoundUseFailure.SIGNED_OUT
        else -> LibrarySoundUseFailure.OTHER
    }
    is AppError.Forbidden -> if (code == CODE_UNAUTHORIZED) LibrarySoundUseFailure.SIGNED_OUT else LibrarySoundUseFailure.OTHER
    else -> LibrarySoundUseFailure.OTHER
}

/** What the library says when its first page cannot be read. */
const val LIBRARY_UNAVAILABLE = "The sound library is not available right now."

private const val CODE_NOT_STORED = "NOT_STORED"
private const val CODE_SOUND_UNAVAILABLE = "SOUND_UNAVAILABLE"
private const val CODE_UNAUTHORIZED = "UNAUTHORIZED"
private const val HTTP_UNPROCESSABLE = 422
private const val HTTP_UNAVAILABLE = 503

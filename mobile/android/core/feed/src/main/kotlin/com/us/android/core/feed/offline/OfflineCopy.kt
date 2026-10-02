package com.us.android.core.feed.offline

import kotlinx.serialization.Serializable

/** What a copy is of. Decides the section it is listed under and the player it opens in. */
@Serializable
enum class OfflineKind { VIDEO, REEL }

/** One stored stream: where it was fetched from, and the key its bytes are kept under. */
@Serializable
data class OfflineStream(
    val key: String,
    /** Absolute `serve` address. Used again only to resume an unfinished fetch. */
    val url: String,
    val mime: String = "",
    /** What the server declared; 0 when it did not say. */
    val sizeBytes: Long = 0L,
)

/** A reel's added sound, stored beside the video and mixed by the player as it is online. */
@Serializable
data class OfflineSound(
    val stream: OfflineStream,
    val startMs: Long = 0L,
    val originalVolume: Double = 1.0,
    val overlayVolume: Double = 1.0,
    /** From the reel as it was when saved, for the sound line; blank when it was not known. */
    val soundId: String = "",
    val title: String = "",
    val artist: String = "",
    val durationMs: Long = 0L,
)

@Serializable
data class OfflineCaption(
    val language: String,
    val label: String,
    val url: String,
    /** The stored WebVTT file, once fetched; null when it could not be. */
    val file: String? = null,
)

/**
 * The post as it was when saved, kept so the copy can be opened with NO
 * network: enough to draw the watch screen and the reel's overlay, and to
 * rebuild the row the players take.
 */
@Serializable
data class OfflinePostSnapshot(
    val authorId: String = "",
    val authorName: String = "",
    val authorUsername: String = "",
    val avatarMediaId: String = "",
    val text: String = "",
    val contentType: String = "",
    val width: Int = 0,
    val height: Int = 0,
    val hashtags: List<String> = emptyList(),
    /** The channel the post was under, when the row carried one; blank otherwise. */
    val channelUserId: String = "",
    val channelHandle: String = "",
)

/**
 * One offline copy on this device (2026-10-02): what the server granted,
 * what was stored of it, and when it must be asked about again.
 *
 * Serializable because it IS the index's row (`OfflineIndex`). Times are
 * epoch milliseconds on this device's clock.
 */
@Serializable
data class OfflineCopy(
    val postId: String,
    val kind: OfflineKind,
    val title: String = "",
    val channelName: String = "",
    val durationMs: Long = 0L,
    /** The server's `expires_at`. Past it the copy is deleted, with or without a network. */
    val expiresAtMs: Long,
    /** The server's `recheck_after_seconds`: how long an answer is good for. */
    val recheckAfterSeconds: Long,
    val grantedAtMs: Long,
    /** When the server last said this copy may be kept (the grant counts). */
    val lastCheckedAtMs: Long,
    /** False while its bytes are still being fetched; true once every stream is whole and verified. */
    val stored: Boolean = false,
    val video: OfflineStream,
    val sound: OfflineSound? = null,
    val captions: List<OfflineCaption> = emptyList(),
    /** The stored poster, for the app's own image loader; never shown as text. */
    val posterFile: String? = null,
    /** Bytes on the device once stored. */
    val sizeBytes: Long = 0L,
    val post: OfflinePostSnapshot = OfflinePostSnapshot(),
    /**
     * The account the copy was granted to. Part of its stream keys and its
     * folder, so two accounts' copies of one post never share bytes. Blank on
     * a copy saved before 2 Oct 2026's sign-out rule, which keeps the keys it
     * was stored under.
     */
    val ownerId: String = "",
    /** When a renewal was last tried, whatever came of it; 0 before the first. */
    val lastRenewAtMs: Long = 0L,
) {
    /** The streams whose bytes make the copy: the video, and the sound when the reel has one. */
    val streams: List<OfflineStream> get() = listOfNotNull(video, sound?.stream)

    /** The folder its poster and caption files are in. */
    val folder: String get() = if (ownerId.isBlank()) postId else "$ownerId-$postId"
}

/** Where one post's copy stands, as a screen reads it. */
enum class OfflinePhase {
    /** The grant is on the wire. */
    REQUESTING,

    /** Bytes are arriving. */
    SAVING,

    /** Held until the allowed network is there. */
    WAITING,

    /** Whole, verified, playable with no network. */
    STORED,
}

/**
 * One post's copy on this device. Absent from [OfflineState.copies] is
 * "idle": no copy and none being made.
 */
data class OfflineEntry(
    val phase: OfflinePhase,
    /** 0..1 while saving; null while the length is not known. */
    val progress: Float? = null,
    /** Null only while [OfflinePhase.REQUESTING]. */
    val copy: OfflineCopy? = null,
)

/** Every copy on this device for the signed-in viewer. */
data class OfflineState(
    val copies: Map<String, OfflineEntry> = emptyMap(),
    /** Everything the copies take on the device. */
    val usedBytes: Long = 0L,
    /** The index has been read; before this an empty [copies] means "not known yet". */
    val loaded: Boolean = false,
) {
    fun phaseOf(postId: String): OfflinePhase? = copies[postId]?.phase
}

/** What Save offline answered. */
sealed interface OfflineSaveResult {
    /** The fetch has begun, or the copy was already there. */
    data object Started : OfflineSaveResult

    /** Granted, and held until Wi-Fi: the viewer's own switch. */
    data object WaitingForWifi : OfflineSaveResult

    /** Refused, with the one line to say. */
    data class Refused(val message: String) : OfflineSaveResult
}

package com.us.android.core.media.offline

import kotlinx.coroutines.flow.StateFlow
import java.io.File

/** One stream to keep: the key it is stored under, and where its bytes come from. */
data class OfflineAsset(
    /** The cache key, and the fetch's id. Stable for the life of the copy. */
    val key: String,
    /** Absolute. A gateway `serve` route: authorized, redirecting to a short-lived signed link. */
    val url: String,
    val mime: String? = null,
)

enum class OfflineFetchState {
    /** Asked for, not started yet. */
    QUEUED,

    /** Held until the network the viewer allows is there (Wi-Fi only, or any network at all). */
    WAITING,
    RUNNING,
    DONE,

    /** Given up after the fetcher's own retries. */
    FAILED,
}

/** Where one asset's fetch stands. [totalBytes] is -1 until the server has said. */
data class OfflineFetch(
    val state: OfflineFetchState,
    val bytes: Long = 0L,
    val totalBytes: Long = UNKNOWN_LENGTH,
) {
    companion object {
        const val UNKNOWN_LENGTH = -1L
    }
}

/**
 * The bytes of offline copies: fetching them into app-private storage,
 * saying how far each fetch is, and removing them (2026-10-02).
 *
 * `:core:media` knows nothing of a post or a grant. `:core:feed` owns the
 * copy state machine and hands this an [OfflineAsset] per stream. An
 * interface so that machine is tested against bytes a test controls.
 *
 * Nothing here returns a location for display. [fetchFile]'s result is for
 * the app's own image loader and caption parser only.
 */
interface OfflineMediaStore {
    /** Every fetch that is not finished and gone, by asset key. */
    val fetches: StateFlow<Map<String, OfflineFetch>>

    /** Starts or resumes [asset]. Asking again for one already complete finishes at once. */
    suspend fun fetch(asset: OfflineAsset)

    /** Stops the fetch and deletes whatever of it was stored. */
    suspend fun remove(key: String)

    /** Every stream, every small file. Sign-out and "Remove all". */
    suspend fun removeAll()

    /** How many of [key]'s bytes are on the device. Equal to its length only when none is missing. */
    suspend fun storedBytes(key: String): Long

    /** The length the server declared for [key] when it was fetched; -1 when it never said. */
    suspend fun declaredBytes(key: String): Long

    /** True: fetch only on an unmetered network. False: on any network. */
    suspend fun setWifiOnly(wifiOnly: Boolean)

    /** A small file (a poster, a caption track) into [folder]; null when it could not be fetched. */
    suspend fun fetchFile(url: String, folder: String, name: String): File?

    /** Deletes [folder]'s small files. */
    suspend fun removeFiles(folder: String)

    /** Everything the copies take on the device. */
    suspend fun usedBytes(): Long

    /** Free space where the copies go. */
    fun usableBytes(): Long
}

/** The network a fetch needs, as Media3's `Requirements` flag. Pure, so the Wi-Fi-only rule is a table test. */
fun offlineNetworkRequirement(wifiOnly: Boolean): Int =
    if (wifiOnly) REQUIREMENT_NETWORK_UNMETERED else REQUIREMENT_NETWORK

/** `Requirements.NETWORK`. */
const val REQUIREMENT_NETWORK = 1

/** `Requirements.NETWORK_UNMETERED`. */
const val REQUIREMENT_NETWORK_UNMETERED = 2

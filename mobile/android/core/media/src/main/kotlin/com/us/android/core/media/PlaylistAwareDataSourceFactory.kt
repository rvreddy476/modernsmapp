package com.us.android.core.media

import android.net.Uri
import androidx.annotation.OptIn
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DataSource
import androidx.media3.datasource.DataSpec
import androidx.media3.datasource.TransferListener

/**
 * Caches media SEGMENTS but never HLS PLAYLISTS.
 *
 * WHY THIS EXISTS
 *
 * Found on a device on 2026-08-18. Caching everything through one
 * `CacheDataSource` looks obviously right and is quietly broken here, because
 * the two kinds of file have opposite lifetimes:
 *
 *  - A segment (`.ts`) is immutable content addressed by a URL. Caching it is
 *    the whole point — scrolling back to a recent reel should replay from disk.
 *  - A playlist (`.m3u8`) is a short-lived DOCUMENT whose body contains signed
 *    segment URLs valid for five minutes. Cache it, and the player keeps
 *    replaying dead links long after they expire.
 *
 * The symptom was a black frame with the object store answering
 * `403 AccessDenied — Request has expired`, and the app making NO gateway
 * request at all: the cached playlist meant it never asked for a fresh one, so
 * it could not discover that its URLs had aged out. A cache that outlives the
 * credentials inside it is worse than no cache, because it removes the very
 * request that would have healed the problem.
 *
 * The rule is therefore about content lifetime, not file type: anything whose
 * body embeds a time-limited capability must be fetched fresh.
 */
@OptIn(UnstableApi::class)
class PlaylistAwareDataSourceFactory(
    private val cacheFactory: DataSource.Factory,
    private val upstreamFactory: DataSource.Factory,
) : DataSource.Factory {

    override fun createDataSource(): DataSource =
        PlaylistAwareDataSource(cacheFactory.createDataSource(), upstreamFactory.createDataSource())
}

@OptIn(UnstableApi::class)
private class PlaylistAwareDataSource(
    private val cached: DataSource,
    private val direct: DataSource,
) : DataSource {

    /**
     * Which delegate served the OPEN call.
     *
     * Held so read/close go to the same one. Routing per-call would read from a
     * source that was never opened, which fails in a way that looks like a
     * corrupt stream rather than a routing bug.
     */
    private var active: DataSource? = null

    override fun open(dataSpec: DataSpec): Long {
        val delegate = if (dataSpec.uri.isPlaylist()) direct else cached
        active = delegate
        return delegate.open(dataSpec)
    }

    override fun read(buffer: ByteArray, offset: Int, length: Int): Int =
        requireNotNull(active) { "read before open" }.read(buffer, offset, length)

    override fun getUri(): Uri? = active?.uri

    override fun getResponseHeaders(): Map<String, List<String>> =
        active?.responseHeaders ?: emptyMap()

    override fun close() {
        try {
            active?.close()
        } finally {
            active = null
        }
    }

    /**
     * Listeners are added to BOTH delegates regardless of which is active.
     *
     * A listener registered only on the current delegate would silently stop
     * reporting the moment a request routed the other way, and bandwidth
     * estimation — which is what drives HLS bitrate selection — would see half
     * the traffic and pick the wrong rung.
     */
    override fun addTransferListener(transferListener: TransferListener) {
        cached.addTransferListener(transferListener)
        direct.addTransferListener(transferListener)
    }
}

/**
 * Matches on the PATH, never the whole URL.
 *
 * Signed URLs carry a long query string, and `X-Amz-Credential` can contain
 * the literal text `s3` and other tokens — matching against the full URL is
 * how a segment gets misclassified as a playlist and stops being cached.
 */
private fun Uri.isPlaylist(): Boolean = isPlaylistPath(path)

/**
 * Pure so it can be tested without Robolectric, and so the rule stays
 * platform-independent for the eventual shared-logic module.
 */
internal fun isPlaylistPath(path: String?): Boolean =
    path?.endsWith(".m3u8", ignoreCase = true) == true

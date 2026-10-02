package com.us.android.core.media

import android.net.Uri
import androidx.annotation.OptIn
import androidx.media3.common.MediaItem
import androidx.media3.common.MimeTypes
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DataSource
import androidx.media3.exoplayer.hls.HlsMediaSource
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import androidx.media3.exoplayer.source.MediaSource
import androidx.media3.exoplayer.source.ProgressiveMediaSource
import com.us.android.core.media.di.OfflineRead
import dagger.Lazy
import javax.inject.Inject
import javax.inject.Singleton

/**
 * One thing a player can open: an absolute URL and how to read it.
 *
 * Two kinds, because instant reels (2026-09-04) made the ORIGINAL upload
 * playable: a flick is created the moment its bytes are confirmed, and until
 * the transcoder has produced an HLS ladder the server hands back the
 * original MP4 as `playback_kind: "original"`. An MP4 through the HLS
 * extractor is a playlist parse error, so the kind travels with the URL
 * rather than being guessed from its suffix — a signed object-store URL has
 * no suffix to guess from.
 *
 * A third since 2026-10-02: [PlaybackKind.Offline], a copy stored on the
 * device. [url] is then only the address the copy was fetched from; the
 * bytes are found by [cacheKey] in the offline cache and nothing is asked of
 * the network. [captions] are the caption tracks stored beside it.
 */
data class Playback(
    val url: String,
    val kind: PlaybackKind,
    /** [PlaybackKind.Offline] only: the key the stored bytes are under. */
    val cacheKey: String? = null,
    /** [PlaybackKind.Offline] only: the stored caption tracks, off until the viewer picks one. */
    val captions: List<PlaybackCaption> = emptyList(),
) {
    companion object {
        fun hls(url: String) = Playback(url, PlaybackKind.Hls)
        fun original(url: String) = Playback(url, PlaybackKind.Progressive)

        /** A copy stored on the device. */
        fun offline(url: String, cacheKey: String, captions: List<PlaybackCaption> = emptyList()) =
            Playback(url, PlaybackKind.Offline, cacheKey, captions)
    }
}

/** One stored caption track: a WebVTT file in the app's private storage. */
data class PlaybackCaption(
    /** A `file:` URI inside the offline directory. Never shown, never shared. */
    val uri: String,
    val language: String,
    val label: String,
)

enum class PlaybackKind { Hls, Progressive, Offline }

/**
 * Builds the media source for a [Playback] over the ONE cached, authenticated
 * data source chain — so the pool and any standalone player fetch bytes the
 * same way. A stored copy is read through the offline chain instead, which
 * has no network behind it.
 */
@Singleton
@OptIn(UnstableApi::class)
class MediaSources @Inject constructor(
    private val dataSourceFactory: DataSource.Factory,
    /** Lazy: the offline cache is not opened until a stored copy is actually played. */
    @OfflineRead private val offlineDataSourceFactory: Lazy<DataSource.Factory>,
) {
    fun create(playback: Playback): MediaSource = when (playback.kind) {
        PlaybackKind.Hls ->
            HlsMediaSource.Factory(dataSourceFactory).createMediaSource(MediaItem.fromUri(playback.url))
        PlaybackKind.Progressive ->
            ProgressiveMediaSource.Factory(dataSourceFactory).createMediaSource(MediaItem.fromUri(playback.url))
        // The default factory, because it is what merges side-loaded caption
        // files into the source; the video itself is still a progressive read.
        PlaybackKind.Offline ->
            DefaultMediaSourceFactory(offlineDataSourceFactory.get()).createMediaSource(offlineItem(playback))
    }

    private fun offlineItem(playback: Playback): MediaItem = MediaItem.Builder()
        .setUri(playback.url)
        .setCustomCacheKey(playback.cacheKey)
        .setSubtitleConfigurations(
            playback.captions.map { caption ->
                MediaItem.SubtitleConfiguration.Builder(Uri.parse(caption.uri))
                    .setMimeType(MimeTypes.TEXT_VTT)
                    .setLanguage(caption.language)
                    .setLabel(caption.label)
                    .build()
            },
        )
        .build()
}

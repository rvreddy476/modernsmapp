package com.us.android.core.media

import androidx.annotation.OptIn
import androidx.media3.common.MediaItem
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DataSource
import androidx.media3.exoplayer.hls.HlsMediaSource
import androidx.media3.exoplayer.source.MediaSource
import androidx.media3.exoplayer.source.ProgressiveMediaSource
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
 */
data class Playback(val url: String, val kind: PlaybackKind) {
    companion object {
        fun hls(url: String) = Playback(url, PlaybackKind.Hls)
        fun original(url: String) = Playback(url, PlaybackKind.Progressive)
    }
}

enum class PlaybackKind { Hls, Progressive }

/**
 * Builds the media source for a [Playback] over the ONE cached, authenticated
 * data source chain — so the pool and any standalone player fetch bytes the
 * same way.
 */
@Singleton
@OptIn(UnstableApi::class)
class MediaSources @Inject constructor(
    private val dataSourceFactory: DataSource.Factory,
) {
    fun create(playback: Playback): MediaSource {
        val item = MediaItem.fromUri(playback.url)
        return when (playback.kind) {
            PlaybackKind.Hls -> HlsMediaSource.Factory(dataSourceFactory).createMediaSource(item)
            PlaybackKind.Progressive -> ProgressiveMediaSource.Factory(dataSourceFactory).createMediaSource(item)
        }
    }
}

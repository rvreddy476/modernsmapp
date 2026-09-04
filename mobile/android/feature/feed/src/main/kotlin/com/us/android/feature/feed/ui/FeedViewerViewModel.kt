package com.us.android.feature.feed.ui

import androidx.lifecycle.ViewModel
import androidx.media3.common.Player
import androidx.media3.exoplayer.ExoPlayer
import com.us.android.core.media.MediaSources
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.media.Playback
import com.us.android.core.media.PlayerFactory
import com.us.android.core.model.FeedItem
import com.us.android.feature.feed.data.playbackFor
import dagger.hilt.android.lifecycle.HiltViewModel
import javax.inject.Inject

/**
 * What the Home feed's in-place viewer needs that a card does not: a
 * playable URL for a video row, and a player to open it with.
 *
 * Its own ViewModel rather than more on [FeedViewModel]: the viewer is one
 * overlay over one row, and the player it makes lives exactly as long as the
 * overlay does — [FeedMediaViewer] releases it on close. The Reels player
 * pool is not used here on purpose: it is owned by `:app` for the Reels
 * pager and keyed by page index, and the feed has no pages to key by.
 */
@HiltViewModel
class FeedViewerViewModel @Inject constructor(
    private val urlResolver: MediaUrlResolver,
    private val playerFactory: PlayerFactory,
    private val sources: MediaSources,
) : ViewModel() {

    /** The row's video, or null for an image post or a video with no rendition yet. */
    fun playback(item: FeedItem): Playback? = urlResolver.playbackFor(item)

    /**
     * A prepared player that starts on its own and loops: the viewer opened
     * this row deliberately, so unlike a reel it plays with sound.
     */
    fun createPlayer(playback: Playback): ExoPlayer = playerFactory.create().apply {
        setMediaSource(sources.create(playback))
        repeatMode = Player.REPEAT_MODE_ONE
        playWhenReady = true
        prepare()
    }
}

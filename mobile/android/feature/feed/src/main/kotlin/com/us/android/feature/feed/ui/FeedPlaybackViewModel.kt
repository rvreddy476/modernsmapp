package com.us.android.feature.feed.ui

import androidx.lifecycle.ViewModel
import androidx.media3.common.C
import androidx.media3.common.Player
import androidx.media3.exoplayer.ExoPlayer
import com.us.android.core.feed.data.hasVideo
import com.us.android.core.feed.data.playbackFor
import com.us.android.core.media.MediaSources
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.media.Playback
import com.us.android.core.media.PlayerFactory
import com.us.android.core.media.ReelsEntry
import com.us.android.core.model.FeedItem
import dagger.hilt.android.lifecycle.HiltViewModel
import javax.inject.Inject

/**
 * What a feed needs to PLAY a video row, as opposed to render it: a playable
 * URL, a silent looping player, and the way to Reels.
 *
 * Its own ViewModel rather than more on [FeedViewModel]: that one is the
 * timeline (which rows, in what order, with what overlays), and this is
 * playback, which every timeline body — Home, Friends, a hashtag's posts,
 * the in-place viewer — does the same way. The players it makes are Android
 * objects the composables own and release; nothing here outlives a screen.
 *
 * The Reels player pool is not used here on purpose: it is owned by `:app`
 * for the Reels pager and keyed by page index, and a feed has no pages to
 * key by. One feed player is enough — the feed plays exactly one video at
 * a time, the most visible one ([mostVisibleVideo]).
 */
@HiltViewModel
class FeedPlaybackViewModel @Inject constructor(
    private val urlResolver: MediaUrlResolver,
    private val playerFactory: PlayerFactory,
    private val sources: MediaSources,
    private val reelsEntry: ReelsEntry,
) : ViewModel() {

    /** The row's video, or null for an image post or a video with no rendition yet. */
    fun playback(item: FeedItem): Playback? = urlResolver.playbackFor(item)

    /** Whether a tap on this row's media goes to Reels rather than the in-place viewer. */
    fun isVideo(item: FeedItem): Boolean = item.hasVideo()

    /**
     * A silent, looping player with nothing loaded — the feed's one player,
     * or a viewer page's. Silent by track selection as well as by volume:
     * the feed never plays sound (founder, 2026-09-05 — sound lives in
     * Reels), so the audio track is not decoded at all, which is cheaper
     * than decoding it into silence and cannot be un-muted by accident.
     */
    fun createSilentPlayer(): ExoPlayer = playerFactory.create().apply {
        repeatMode = Player.REPEAT_MODE_ONE
        volume = 0f
        trackSelectionParameters = trackSelectionParameters.buildUpon()
            .setTrackTypeDisabled(C.TRACK_TYPE_AUDIO, true)
            .build()
    }

    /** Points [player] at [playback] and prepares it; whether it plays is the caller's. */
    fun load(player: ExoPlayer, playback: Playback) {
        player.setMediaSource(sources.create(playback))
        player.prepare()
    }

    /**
     * A feed video was tapped: Reels should open on it. The tab switch
     * itself is the shell's ([FeedContent]'s `onOpenReels`); this only
     * leaves the id where Reels will find it.
     */
    fun openInReels(item: FeedItem) = reelsEntry.open(item.id)
}

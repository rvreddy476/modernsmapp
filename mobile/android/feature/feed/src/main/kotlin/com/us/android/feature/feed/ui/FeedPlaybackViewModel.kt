package com.us.android.feature.feed.ui

import androidx.lifecycle.ViewModel
import com.us.android.core.feed.data.playbackFor
import com.us.android.core.media.MediaSources
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.media.Playback
import com.us.android.core.media.PlayerFactory
import com.us.android.core.media.PlayerPool
import com.us.android.core.model.FeedItem
import dagger.hilt.android.lifecycle.HiltViewModel
import javax.inject.Inject

/**
 * What a full-screen feed needs to PLAY a video post, as opposed to render
 * it: a playable URL and players to play it on.
 *
 * Its own ViewModel rather than more on [FeedViewModel]: that one is the
 * timeline (which posts, in what order, with what overlays), and this is
 * playback, which every timeline body — Home, Friends, a hashtag's posts —
 * does the same way.
 *
 * ## A SECOND PLAYER POOL, NOT THE REELS TAB'S
 *
 * Home is a pager of full-screen posts now (2026-10-18), so it plays the way
 * Reels does: the settled page plays, the neighbours are prepared, and
 * players are recycled by page through a [PlayerPool]. It is THIS screen's
 * own pool, built from the app's player factory and authenticated media
 * chain — the same pattern as the chat's shared-reels viewer — never the
 * Reels tab's: `:app` owns that one for the Reels pager, and two surfaces
 * keying one pool by page index would hand each other's videos out.
 *
 * The body releases its pool whenever it leaves the composition (a tab
 * switch, a pushed screen) and makes a new one on return. Holding four
 * decoders under another tab that holds four more is how the next video
 * stops playing at all.
 */
@HiltViewModel
class FeedPlaybackViewModel @Inject constructor(
    private val urlResolver: MediaUrlResolver,
    private val playerFactory: PlayerFactory,
    private val sources: MediaSources,
) : ViewModel() {

    /**
     * A pool for one timeline body, which releases it when it leaves the
     * composition. Per body rather than per ViewModel: Home's For You and
     * Following share this ViewModel, and the body leaving must not release
     * the players the body arriving has just acquired.
     */
    fun newPool(): PlayerPool = PlayerPool(playerFactory, sources)

    /** The post's video, or null for a photo or text post, or a video with no rendition yet. */
    fun playback(item: FeedItem): Playback? = urlResolver.playbackFor(item)
}

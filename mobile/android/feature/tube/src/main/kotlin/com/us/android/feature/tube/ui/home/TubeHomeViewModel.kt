package com.us.android.feature.tube.ui.home

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.paging.PagingData
import androidx.paging.cachedIn
import androidx.paging.filter
import com.us.android.core.common.result.AppResult
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.engagement.data.EngagementStore
import com.us.android.core.engagement.data.HiddenPosts
import com.us.android.core.feed.data.ChannelRepository
import com.us.android.core.feed.data.ChannelState
import com.us.android.core.feed.data.ContinueWatching
import com.us.android.core.feed.data.FollowGraph
import com.us.android.core.feed.data.VideoFeedRepository
import com.us.android.core.feed.data.VideoThumb
import com.us.android.core.feed.data.hides
import com.us.android.core.feed.data.videoThumb
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.media.ReelsEntry
import com.us.android.core.media.data.MediaRepository
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FollowStatus
import com.us.android.feature.tube.data.TubeQueue
import com.us.android.feature.tube.ui.TubeViewer
import com.us.android.feature.tube.ui.TubeViewerStore
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.flatMapLatest
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * The extras around the ranked mosaic. Each is a separate read and each
 * fails alone: an empty list is simply not drawn.
 */
data class TubeShelves(
    val continueWatching: List<ContinueWatching> = emptyList(),
    val reels: List<FeedItem> = emptyList(),
    val channels: List<TubeChannelBubble> = emptyList(),
)

/**
 * Tube home (Momentum layout, 2026-09-05): the chip rail's query paged
 * through the video surface, the channels strip from the Following feed,
 * the two shelves, and the viewer's own channel for the strip's first
 * bubble.
 *
 * A video the viewer is posting no longer shows here: since 2026-09-05
 * Post lands on the viewer's own profile, whose grid draws the pending
 * tile with its ring. Tube reads the finished post from the feed like any
 * other.
 */
@HiltViewModel
// Constructor injection of the surface's collaborators; a wrapper would add
// indirection, not clarity.
@Suppress("LongParameterList")
@OptIn(ExperimentalCoroutinesApi::class)
class TubeHomeViewModel @Inject constructor(
    private val savedStateHandle: SavedStateHandle,
    private val videos: VideoFeedRepository,
    private val urlResolver: MediaUrlResolver,
    private val media: MediaRepository,
    private val queue: TubeQueue,
    private val reelsEntry: ReelsEntry,
    private val channels: ChannelRepository,
    private val viewerStore: TubeViewerStore,
    private val follows: FollowGraph,
    engagement: EngagementStore,
    hidden: HiddenPosts,
) : ViewModel() {

    /** The rail: All and Following at once, the taxonomy once it loads. */
    private val _chips = MutableStateFlow(tubeChips(emptyList()))
    val chips: StateFlow<List<TubeChip>> = _chips

    /** The selected chip's key survives process death; the chip itself is resolved against the rail. */
    private val selectedKey = MutableStateFlow(savedStateHandle.get<String>(KEY_CHIP) ?: TubeChip.All.key)
    val selected: StateFlow<TubeChip> = combine(_chips, selectedKey) { rail, key -> rail.chipFor(key) }
        .stateIn(viewModelScope, SharingStarted.Eagerly, TubeChip.All)

    private val _shelves = MutableStateFlow(TubeShelves())
    val shelves: StateFlow<TubeShelves> = _shelves

    /** The viewer's own channel, for the strip's first bubble: theirs, or "Create". */
    val ownChannel: StateFlow<ChannelState> = channels.own

    /** The viewer, for the "You" bubble's face. */
    val viewer: StateFlow<TubeViewer?> = viewerStore.viewer

    /**
     * The ranked videos for the selected chip, one cached stream per
     * selection so rotation replays the pages rather than refetching.
     */
    val items: Flow<PagingData<FeedItem>> = selectedKey
        .map { key -> _chips.value.chipFor(key).toQuery() }
        .flatMapLatest { query -> videos.videos(query).cachedIn(viewModelScope) }
        .combine(hidden.state) { page, set ->
            // "Not interested", Block and Delete from the more sheet — removed
            // at once, the same way every feed removes them.
            if (set.isEmpty) page else page.filter { !set.hides(it) }
        }

    // ── Engagement, the shared lanes (for the "more" sheet) ──────────────

    val overlays: StateFlow<Map<String, EngagementOverlay>> = engagement.overlays
    val followEdges: StateFlow<Map<String, FollowStatus>> = follows.edges
    val ownUserId: String get() = follows.ownId

    init {
        viewModelScope.launch { _chips.value = tubeChips(videos.categories()) }
        viewModelScope.launch { channels.ensureLoaded() }
        viewModelScope.launch { viewerStore.ensureLoaded() }
        refreshShelves()
    }

    /** A chip was tapped: the list reloads for it. */
    fun select(chip: TubeChip) {
        savedStateHandle[KEY_CHIP] = chip.key
        selectedKey.value = chip.key
    }

    /** Pull-to-refresh reloads the shelves and the channel too; the ranked list refreshes through Paging. */
    fun refreshShelves() {
        viewModelScope.launch {
            coroutineScope {
                val continueWatching = async { videos.continueWatching(CONTINUE_LIMIT) }
                val reels = async { videos.reels(REELS_LIMIT) }
                val strip = async { loadChannels() }
                val channel = async { channels.refresh() }
                _shelves.value = TubeShelves(
                    continueWatching = continueWatching.await(),
                    reels = reels.await(),
                    channels = strip.await(),
                )
                channel.await()
            }
        }
    }

    /**
     * The strip: followed creators with videos, newest first, each with a
     * face — the channel's own when the row carries one, else the author's
     * profile photo resolved here (a handful of lookups, once per refresh).
     */
    private suspend fun loadChannels(): List<TubeChannelBubble> {
        val bubbles = channelBubbles(videos.followingVideos(FOLLOWING_LIMIT), ownUserId)
        return coroutineScope {
            bubbles.map { bubble ->
                async {
                    val id = bubble.avatarMediaId
                    if (bubble.avatarUrl != null || id.isNullOrBlank()) {
                        bubble
                    } else {
                        val url = (media.delivery(id) as? AppResult.Success)?.data?.takeIf { it.isReady }?.posterUrl
                        bubble.copy(avatarUrl = url)
                    }
                }
            }.awaitAll()
        }
    }

    /** What the card draws before its video: the still, the wash, the length. */
    fun thumb(item: FeedItem): VideoThumb = urlResolver.videoThumb(item)

    /**
     * A card was tapped: the list as the viewer sees it — the loaded rows —
     * becomes what the watch screen plays through, so "Up next" is the rows
     * under the tapped one.
     */
    fun onOpen(loaded: List<FeedItem>) = queue.set(loaded)

    /** A continue-watching card was tapped: the shelf is the queue, so "Up next" is the rest of it. */
    fun onOpenContinue(item: FeedItem) {
        queue.set(_shelves.value.continueWatching.map { it.item }.sortedBy { it.id != item.id })
    }

    /** A reel was tapped: leave the id for Reels to open on, the way the Home feed does. */
    fun openInReels(item: FeedItem) = reelsEntry.open(item.id)

    private companion object {
        const val KEY_CHIP = "tube_chip"

        /** A shelf's worth: enough to scroll, not a page of fetches per card. */
        const val CONTINUE_LIMIT = 10
        const val REELS_LIMIT = 10

        /** One page of the Following feed is plenty of authors for a strip. */
        const val FOLLOWING_LIMIT = 30
    }
}

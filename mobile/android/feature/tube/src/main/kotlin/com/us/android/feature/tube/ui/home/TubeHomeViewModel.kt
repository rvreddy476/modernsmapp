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
import com.us.android.core.feed.data.FeedRepository
import com.us.android.core.feed.data.FollowGraph
import com.us.android.core.feed.data.VideoFeedRepository
import com.us.android.core.feed.data.VideoThumb
import com.us.android.core.feed.data.hides
import com.us.android.core.feed.data.videoThumb
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.media.ReelsEntry
import com.us.android.core.media.TubeEntry
import com.us.android.core.media.data.MediaRepository
import com.us.android.core.media.publish.playsInReels
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
import kotlinx.coroutines.delay
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

/** Whether a row is short enough to be a reel — the app's one five-minute rule. */
private fun FeedItem.isReelLength(): Boolean =
    playsInReels(feedContentType, media.maxOfOrNull { it.durationMs } ?: 0L)

/**
 * Tube home (Momentum layout, 2026-09-05): the chip rail's query paged
 * through the video surface, the channels strip from the Following feed,
 * the two shelves, and the viewer's own channel for the strip's first
 * bubble.
 *
 * A video the viewer is posting no longer shows here: since 2026-09-05
 * Post lands on the viewer's own profile, whose grid draws the pending
 * tile with its ring. Tube reads the finished post from the feed like any
 * other — and since 2026-09-06 the profile SENDS the viewer here when that
 * upload finishes, with the video's id in [TubeEntry]; see [pinned].
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
    private val tubeEntry: TubeEntry,
    private val posts: FeedRepository,
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

    private val _pinned = MutableStateFlow<FeedItem?>(null)

    /**
     * The video the viewer has just finished posting, above the ranked rows
     * (founder, 2026-09-06: a long video's journey ends "on the Tube home
     * page with that video there").
     *
     * A PIN and not a refresh, exactly as the home feed's is: the ranked
     * mosaic decides its own order and a brand-new video may not be in its
     * first page at all, so the post is fetched by id and drawn first, and
     * filtered out of the paged rows so it never appears twice. Null on an
     * ordinary visit, and again as soon as the viewer leaves and returns.
     */
    val pinned: StateFlow<FeedItem?> = _pinned

    /**
     * The ranked videos for the selected chip, one cached stream per
     * selection so rotation replays the pages rather than refetching.
     */
    val items: Flow<PagingData<FeedItem>> = selectedKey
        .map { key -> _chips.value.chipFor(key).toQuery() }
        .flatMapLatest { query -> videos.videos(query).cachedIn(viewModelScope) }
        .combine(_pinned) { page, first ->
            if (first == null) page else page.filter { it.id != first.id }
        }
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
        takeEntry()
        refreshShelves()
    }

    /**
     * The publish journey's last hop: the profile left a post id in
     * [TubeEntry] and sent the viewer here. The request is cleared FIRST, so
     * a fetch that fails leaves an ordinary Tube home rather than a page
     * that keeps trying; post-service can lag the worker's answer by a beat,
     * so the read is retried a few times before it is given up on — the same
     * allowance the Reels head makes for a just-published reel.
     */
    private fun takeEntry() {
        val postId = tubeEntry.first.value ?: return
        tubeEntry.clear()
        viewModelScope.launch {
            repeat(PIN_FETCH_ATTEMPTS) { attempt ->
                when (val result = posts.post(postId)) {
                    is AppResult.Success -> {
                        _pinned.value = result.data
                        return@launch
                    }
                    is AppResult.Failure -> if (attempt < PIN_FETCH_ATTEMPTS - 1) delay(PIN_FETCH_RETRY_MILLIS)
                }
            }
        }
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
                    // The same five-minute rule the Reels feed applies
                    // (founder, 2026-09-06): a mistagged long video is not a
                    // reel and does not belong on a shelf called Reels.
                    reels = reels.await().filter { it.isReelLength() },
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

        /** post-service can lag the worker's answer by a beat; three tries a second apart covers it. */
        const val PIN_FETCH_ATTEMPTS = 3
        const val PIN_FETCH_RETRY_MILLIS = 1_000L
    }
}

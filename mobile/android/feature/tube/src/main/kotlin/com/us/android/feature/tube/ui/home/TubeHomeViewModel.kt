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
import com.us.android.core.feed.data.ContinueWatching
import com.us.android.core.feed.data.FeedRepository
import com.us.android.core.feed.data.FollowGraph
import com.us.android.core.feed.data.VideoFeedRepository
import com.us.android.core.feed.data.hides
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.media.ReelsEntry
import com.us.android.core.media.publish.ReelPublishActions
import com.us.android.core.media.publish.ReelPublishState
import com.us.android.core.media.publish.ReelPublishTracker
import com.us.android.core.media.publish.VideoKind
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FollowStatus
import com.us.android.feature.tube.data.TubeQueue
import com.us.android.feature.tube.ui.VideoThumb
import com.us.android.feature.tube.ui.videoThumb
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.async
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
 * The slot ABOVE the ranked videos: the viewer's own long video while it
 * posts, and the real post the moment the server has created it — the same
 * shape as the Reels head, for the same reason: the item IS the progress.
 */
sealed interface TubeHead {
    /** Still posting (or stopped). The cover the user chose, the title, the caption. */
    data class Pending(
        val creationKey: String,
        val coverPath: String?,
        val title: String,
        val caption: String,
        /** Null while work is in flight; set when the publish stopped. */
        val failure: TubePendingFailure? = null,
    ) : TubeHead

    /** The post exists. Rendered as every other card, and it opens. */
    data class Live(val item: FeedItem) : TubeHead
}

data class TubePendingFailure(val message: String, val retryable: Boolean)

/**
 * The two shelves between the ranked cards. Both are extras: either may be
 * empty, and an empty shelf is simply not drawn.
 */
data class TubeShelves(
    val continueWatching: List<ContinueWatching> = emptyList(),
    val shorts: List<FeedItem> = emptyList(),
)

/**
 * Tube home (redesign, 2026-09-05): the chip rail's query paged through
 * the video surface, the two shelves, and the viewer's own long video at
 * the head while it posts.
 *
 * The publish tracker is process-wide and shared with Reels; the two
 * surfaces split it by [VideoKind] — a reel posting never shows here and a
 * long video never shows in Reels — so one worker and one persisted record
 * serve both without either drawing the other's pending item.
 */
@HiltViewModel
// Constructor injection of the surface's collaborators; a wrapper would add
// indirection, not clarity.
@Suppress("LongParameterList")
@OptIn(ExperimentalCoroutinesApi::class)
class TubeHomeViewModel @Inject constructor(
    private val savedStateHandle: SavedStateHandle,
    private val videos: VideoFeedRepository,
    private val repository: FeedRepository,
    private val urlResolver: MediaUrlResolver,
    private val tracker: ReelPublishTracker,
    private val publishActions: ReelPublishActions,
    private val queue: TubeQueue,
    private val reelsEntry: ReelsEntry,
    private val engagement: EngagementStore,
    private val follows: FollowGraph,
    hidden: HiddenPosts,
) : ViewModel() {

    /** The rail: All and Following at once, the taxonomy once it loads. */
    private val _chips = MutableStateFlow(tubeChips(emptyList()))
    val chips: StateFlow<List<TubeChip>> = _chips

    /** The selected chip's key survives process death; the chip itself is resolved against the rail. */
    private val selectedKey = MutableStateFlow(savedStateHandle.get<String>(KEY_CHIP) ?: TubeChip.All.key)
    val selected: StateFlow<TubeChip> = combine(_chips, selectedKey) { rail, key -> rail.chipFor(key) }
        .stateIn(viewModelScope, SharingStarted.Eagerly, TubeChip.All)

    /** The long video this session just published, once fetched — pinned above the ranked rows. */
    private val _live = MutableStateFlow<FeedItem?>(null)

    private val _shelves = MutableStateFlow(TubeShelves())
    val shelves: StateFlow<TubeShelves> = _shelves

    /**
     * The ranked videos for the selected chip, one cached stream per
     * selection so rotation replays the pages rather than refetching. A
     * video that went live this session leaves the ranked page once the
     * feed carries it: the head already shows it.
     */
    val items: Flow<PagingData<FeedItem>> = selectedKey
        .map { key -> _chips.value.chipFor(key).toQuery() }
        .flatMapLatest { query -> videos.videos(query).cachedIn(viewModelScope) }
        .combine(_live) { page, live ->
            if (live == null) page else page.filter { it.id != live.id }
        }
        .combine(hidden.state) { page, set ->
            // "Not interested", Block and Delete from the more sheet — removed
            // at once, the same way every feed removes them.
            if (set.isEmpty) page else page.filter { !set.hides(it) }
        }

    /** The slot above the list: nothing, the pending card, or the live post. */
    val head: StateFlow<TubeHead?> = combine(tracker.state, tracker.preview, _live) { state, preview, live ->
        when {
            live != null -> TubeHead.Live(live)
            preview == null || preview.kind != VideoKind.LONG || state is ReelPublishState.Idle -> null
            else -> TubeHead.Pending(
                creationKey = preview.creationKey,
                coverPath = preview.coverPath,
                title = preview.title,
                caption = preview.caption,
                failure = (state as? ReelPublishState.Failed)?.let { TubePendingFailure(it.message, it.retryable) },
            )
        }
    }.stateIn(viewModelScope, SharingStarted.WhileSubscribed(STOP_TIMEOUT_MILLIS), null)

    // ── Engagement, the shared lanes (for the "more" sheet) ──────────────

    val overlays: StateFlow<Map<String, EngagementOverlay>> = engagement.overlays
    val followEdges: StateFlow<Map<String, FollowStatus>> = follows.edges
    val ownUserId: String get() = follows.ownId

    init {
        viewModelScope.launch { _chips.value = tubeChips(videos.categories()) }
        refreshShelves()
        // The moment the worker reports the post id, fetch the post the server
        // made of it and let the tracker go — the pending card becomes the
        // real thing without a refresh.
        viewModelScope.launch {
            tracker.state.collect { state ->
                if (state is ReelPublishState.Published && tracker.preview.value?.kind == VideoKind.LONG) {
                    becomeLive(state.postId)
                }
            }
        }
    }

    /** A chip was tapped: the list reloads for it. */
    fun select(chip: TubeChip) {
        savedStateHandle[KEY_CHIP] = chip.key
        selectedKey.value = chip.key
    }

    /** Pull-to-refresh reloads the shelves too; the ranked list refreshes through Paging. */
    fun refreshShelves() {
        viewModelScope.launch {
            val continueWatching = async { videos.continueWatching(CONTINUE_LIMIT) }
            val shorts = async { videos.shorts(SHORTS_LIMIT) }
            _shelves.value = TubeShelves(continueWatching = continueWatching.await(), shorts = shorts.await())
        }
    }

    private suspend fun becomeLive(postId: String) {
        if (_live.value?.id == postId) return
        repeat(LIVE_FETCH_ATTEMPTS) { attempt ->
            when (val result = repository.post(postId)) {
                is AppResult.Success -> {
                    _live.value = result.data
                    publishActions.dismiss()
                    return
                }
                is AppResult.Failure -> if (attempt < LIVE_FETCH_ATTEMPTS - 1) delay(LIVE_FETCH_RETRY_MILLIS)
            }
        }
        // The post exists even if this client could not read it back yet; the
        // next refresh carries it. A loader over a finished publish would lie.
        publishActions.dismiss()
    }

    fun retryPublish() = publishActions.retry()

    fun discardPublish() = publishActions.discard()

    /** What the card draws before its video: the still, the wash, the length. */
    fun thumb(item: FeedItem): VideoThumb = urlResolver.videoThumb(item)

    /**
     * A card was tapped: the list as the viewer sees it — the live head
     * first, then the loaded rows — becomes what the watch screen plays
     * through, so "Up next" is the rows under the tapped one.
     */
    fun onOpen(loaded: List<FeedItem>) {
        queue.set(listOfNotNull(_live.value) + loaded)
    }

    /** A continue-watching card was tapped: the shelf is the queue, so "Up next" is the rest of it. */
    fun onOpenContinue(item: FeedItem) {
        queue.set(_shelves.value.continueWatching.map { it.item }.sortedBy { it.id != item.id })
    }

    /** A short was tapped: leave the id for Reels to open on, the way the Home feed does. */
    fun openInReels(item: FeedItem) = reelsEntry.open(item.id)

    private companion object {
        const val KEY_CHIP = "tube_chip"
        const val STOP_TIMEOUT_MILLIS = 5_000L

        /** post-service can lag the worker's answer by a beat; three tries a second apart covers it. */
        const val LIVE_FETCH_ATTEMPTS = 3
        const val LIVE_FETCH_RETRY_MILLIS = 1_000L

        /** A shelf's worth: enough to scroll, not a page of fetches per card. */
        const val CONTINUE_LIMIT = 10
        const val SHORTS_LIMIT = 10
    }
}

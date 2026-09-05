package com.us.android.feature.tube.ui.you

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.paging.PagingData
import androidx.paging.cachedIn
import androidx.paging.filter
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
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FollowStatus
import com.us.android.feature.tube.data.TubeQueue
import com.us.android.feature.tube.ui.TubeViewer
import com.us.android.feature.tube.ui.TubeViewerStore
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * The You page (Momentum look, 2026-09-05): the viewer's channel header —
 * or the invitation to create one — then their own long videos as the
 * mosaic, what they left unfinished, and what they saved. Each list is its
 * own read and each fails alone — a Saved list that 404s is a Saved section
 * that is absent, never a page that is.
 */
@HiltViewModel
// Constructor injection of the page's collaborators; a wrapper would add
// indirection, not clarity.
@Suppress("LongParameterList")
class YouViewModel @Inject constructor(
    private val videos: VideoFeedRepository,
    private val channels: ChannelRepository,
    private val viewerStore: TubeViewerStore,
    private val urlResolver: MediaUrlResolver,
    private val queue: TubeQueue,
    private val follows: FollowGraph,
    engagement: EngagementStore,
    hidden: HiddenPosts,
) : ViewModel() {

    /** The viewer's channel: theirs, none yet, unknown, or a lookup that failed (with Retry). */
    val channel: StateFlow<ChannelState> = channels.own

    /** The viewer's name and photo, for the header while the channel loads and for the create prompt. */
    val viewer: StateFlow<TubeViewer?> = viewerStore.viewer

    private val _continueWatching = MutableStateFlow<List<ContinueWatching>?>(null)

    /** Null while loading; empty when there is nothing unfinished. */
    val continueWatching: StateFlow<List<ContinueWatching>?> = _continueWatching.asStateFlow()

    val ownVideos: Flow<PagingData<FeedItem>> = videos.ownVideos(follows.ownId)
        .cachedIn(viewModelScope)
        .combine(hidden.state) { page, set ->
            // Delete from the more sheet removes the row at once.
            if (set.isEmpty) page else page.filter { !set.hides(it) }
        }

    val saved: Flow<PagingData<FeedItem>> = videos.savedVideos()
        .cachedIn(viewModelScope)
        .combine(hidden.state) { page, set ->
            if (set.isEmpty) page else page.filter { !set.hides(it) }
        }

    val overlays: StateFlow<Map<String, EngagementOverlay>> = engagement.overlays
    val followEdges: StateFlow<Map<String, FollowStatus>> = follows.edges
    val ownUserId: String get() = follows.ownId

    init {
        viewModelScope.launch { channels.ensureLoaded() }
        viewModelScope.launch { viewerStore.ensureLoaded() }
        viewModelScope.launch { _continueWatching.value = videos.continueWatching(CONTINUE_LIMIT) }
    }

    /** The channel lookup failed: ask again. */
    fun retryChannel() {
        viewModelScope.launch { channels.refresh() }
    }

    fun thumb(item: FeedItem): VideoThumb = urlResolver.videoThumb(item)

    /** A card was tapped: the list it sits in becomes the watch screen's queue, that card first. */
    fun onOpen(item: FeedItem, from: List<FeedItem>) {
        queue.set(from.sortedBy { it.id != item.id })
    }

    private companion object {
        const val CONTINUE_LIMIT = 20
    }
}

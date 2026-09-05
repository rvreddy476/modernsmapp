package com.us.android.feature.tube.ui.channel

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.navigation.toRoute
import androidx.paging.PagingData
import androidx.paging.cachedIn
import androidx.paging.filter
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.engagement.data.EngagementStore
import com.us.android.core.engagement.data.HiddenPosts
import com.us.android.core.feed.data.ChannelRepository
import com.us.android.core.feed.data.FollowGraph
import com.us.android.core.feed.data.VideoFeedRepository
import com.us.android.core.feed.data.VideoThumb
import com.us.android.core.feed.data.hides
import com.us.android.core.feed.data.videoThumb
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.model.Channel
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FollowStatus
import com.us.android.feature.tube.data.TubeQueue
import com.us.android.feature.tube.navigation.TubeChannelRoute
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.launch
import javax.inject.Inject

/** The channel page's header: the channel, or why it is not there. */
sealed interface ChannelHeaderState {
    data object Loading : ChannelHeaderState
    data class Loaded(val channel: Channel) : ChannelHeaderState

    /** The server has no channel for this user — they post no long videos, or predate channels. */
    data object Missing : ChannelHeaderState
    data class Failed(val message: String) : ChannelHeaderState
}

/**
 * A channel inside Tube (2026-09-05): `GET v1/channels/{user_id}` for the
 * header and the follow state from the shared graph, then that user's
 * long videos through the same paged read the You page uses.
 */
@HiltViewModel
// Constructor injection of the page's collaborators; a wrapper would add
// indirection, not clarity.
@Suppress("LongParameterList")
class ChannelViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    videos: VideoFeedRepository,
    private val channels: ChannelRepository,
    private val urlResolver: MediaUrlResolver,
    private val queue: TubeQueue,
    private val follows: FollowGraph,
    engagement: EngagementStore,
    hidden: HiddenPosts,
) : ViewModel() {

    val userId: String = savedStateHandle.toRoute<TubeChannelRoute>().userId

    private val _header = MutableStateFlow<ChannelHeaderState>(ChannelHeaderState.Loading)
    val header: StateFlow<ChannelHeaderState> = _header.asStateFlow()

    val items: Flow<PagingData<FeedItem>> = videos.ownVideos(userId)
        .cachedIn(viewModelScope)
        .combine(hidden.state) { page, set ->
            if (set.isEmpty) page else page.filter { !set.hides(it) }
        }

    val overlays: StateFlow<Map<String, EngagementOverlay>> = engagement.overlays
    val followEdges: StateFlow<Map<String, FollowStatus>> = follows.edges
    val ownUserId: String get() = follows.ownId

    /** Follow / unfollow in flight, so the button does not take a second tap. */
    private val _followBusy = MutableStateFlow(false)
    val followBusy: StateFlow<Boolean> = _followBusy.asStateFlow()

    init {
        load()
        viewModelScope.launch { follows.ensureKnown(listOf(userId)) }
    }

    fun load() {
        _header.value = ChannelHeaderState.Loading
        viewModelScope.launch {
            _header.value = when (val result = channels.channel(userId)) {
                is AppResult.Success -> ChannelHeaderState.Loaded(result.data)
                is AppResult.Failure -> when (result.error) {
                    is AppError.NotFound -> ChannelHeaderState.Missing
                    is AppError.NoNetwork -> ChannelHeaderState.Failed("You're offline. Check your connection.")
                    else -> ChannelHeaderState.Failed("We couldn't load this channel.")
                }
            }
        }
    }

    fun follow() {
        if (_followBusy.value) return
        viewModelScope.launch {
            _followBusy.value = true
            follows.follow(userId)
            _followBusy.value = false
        }
    }

    fun unfollow() {
        if (_followBusy.value) return
        viewModelScope.launch {
            _followBusy.value = true
            follows.unfollow(userId)
            _followBusy.value = false
        }
    }

    fun thumb(item: FeedItem): VideoThumb = urlResolver.videoThumb(item)

    /** A tile was tapped: the channel's loaded videos are the queue. */
    fun onOpen(loaded: List<FeedItem>) = queue.set(loaded)
}

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
import com.us.android.core.feed.data.SubscriptionGraph
import com.us.android.core.feed.data.VideoFeedRepository
import com.us.android.core.feed.data.VideoThumb
import com.us.android.core.feed.data.hides
import com.us.android.core.feed.data.videoThumb
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.model.Channel
import com.us.android.core.model.ChannelSubscription
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FollowStatus
import com.us.android.core.model.NotifyOn
import com.us.android.feature.tube.data.TubeQueue
import com.us.android.feature.tube.navigation.TubeChannelRoute
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
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
 * A channel inside Tube (2026-09-05; subscriptions 2026-09-12):
 * `GET v1/channels/{user_id}` for the header, the subscription edge from
 * the shared [SubscriptionGraph], and that user's long videos through the
 * same paged read the You page uses.
 *
 * Subscribe is the page's one relationship control (founder): it is
 * follow plus notify, made by the SERVER in one call, so this view model
 * never sends a follow of its own. The follow graph is still injected
 * because the "more" sheet over a video card reads it, and because the
 * server's follow lands there on its next read.
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
    private val subscriptions: SubscriptionGraph,
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
    val ownUserId: String get() = subscriptions.ownId

    /** Whether the viewer was subscribed when the header last loaded, the baseline the count is moved from. */
    private val loadedSubscribed = MutableStateFlow(false)

    /** The viewer's subscription toward this channel; null until the graph knows. */
    val subscription: StateFlow<ChannelSubscription?> = subscriptions.edges
        .map { it[userId] }
        .stateIn(viewModelScope, SharingStarted.Eagerly, subscriptions.edges.value[userId])

    /**
     * The count the header shows: the server's number as of the last load,
     * moved by one for the viewer's own subscribe or unsubscribe since. The
     * server count already includes the viewer when they arrived subscribed,
     * so the delta is measured against the edge AT LOAD, not against zero.
     */
    val subscriberCount: StateFlow<Int> = combine(_header, subscription, loadedSubscribed) { header, edge, atLoad ->
        val base = (header as? ChannelHeaderState.Loaded)?.channel?.subscriberCount ?: 0
        val now = edge?.subscribed ?: atLoad
        (base + (if (now) 1 else 0) - (if (atLoad) 1 else 0)).coerceAtLeast(0)
    }.stateIn(viewModelScope, SharingStarted.Eagerly, 0)

    /** A subscribe, unsubscribe or bell change in flight, so the control does not take a second tap. */
    private val _subscribeBusy = MutableStateFlow(false)
    val subscribeBusy: StateFlow<Boolean> = _subscribeBusy.asStateFlow()

    init {
        load()
        viewModelScope.launch { follows.ensureKnown(listOf(userId)) }
    }

    fun load() {
        _header.value = ChannelHeaderState.Loading
        viewModelScope.launch {
            _header.value = when (val result = channels.read(userId)) {
                is AppResult.Success -> {
                    recordSubscription(result.data.subscription)
                    ChannelHeaderState.Loaded(result.data.channel)
                }
                is AppResult.Failure -> when (result.error) {
                    is AppError.NotFound -> ChannelHeaderState.Missing
                    is AppError.NoNetwork -> ChannelHeaderState.Failed("You're offline. Check your connection.")
                    else -> ChannelHeaderState.Failed("We couldn't load this channel.")
                }
            }
        }
    }

    /**
     * The channel read carries the viewer's edge for a signed-in caller;
     * the graph records it so the button is right on the first frame. A
     * public read carries nothing, and the graph asks its own endpoint.
     */
    private suspend fun recordSubscription(edge: ChannelSubscription?) {
        if (edge != null) {
            subscriptions.record(userId, edge)
        } else {
            subscriptions.ensureKnown(listOf(userId))
        }
        loadedSubscribed.value = subscriptions.edges.value[userId]?.subscribed ?: false
    }

    fun subscribe() = relationshipChange { subscriptions.subscribe(userId) }

    fun unsubscribe() = relationshipChange { subscriptions.unsubscribe(userId) }

    /** The bell: all → none, none → all. Only meaningful while subscribed. */
    fun toggleNotify() {
        val current = subscription.value ?: return
        if (!current.subscribed) return
        val next = if (current.notifyOn == NotifyOn.ALL) NotifyOn.NONE else NotifyOn.ALL
        relationshipChange { subscriptions.setNotifyOn(userId, next) }
    }

    private fun relationshipChange(change: suspend () -> AppResult<Unit>) {
        if (_subscribeBusy.value) return
        viewModelScope.launch {
            _subscribeBusy.value = true
            change()
            _subscribeBusy.value = false
        }
    }

    fun thumb(item: FeedItem): VideoThumb = urlResolver.videoThumb(item)

    /** A tile was tapped: the channel's loaded videos are the queue. */
    fun onOpen(loaded: List<FeedItem>) = queue.set(loaded)
}

package com.us.android.feature.tube.ui.subscriptions

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.paging.PagingData
import androidx.paging.cachedIn
import androidx.paging.filter
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.engagement.data.EngagementStore
import com.us.android.core.engagement.data.HiddenPosts
import com.us.android.core.feed.data.FollowGraph
import com.us.android.core.feed.data.VideoFeedQuery
import com.us.android.core.feed.data.VideoFeedRepository
import com.us.android.core.feed.data.hides
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FollowStatus
import com.us.android.feature.tube.data.TubeQueue
import com.us.android.feature.tube.ui.VideoThumb
import com.us.android.feature.tube.ui.videoThumb
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import javax.inject.Inject

/**
 * The Subscriptions page: `v1/feed/watch?following_only=true` — long
 * videos from authors the viewer follows, as the server orders them. The
 * same cards as home, without the shelves; the same "more" sheet with
 * `suggested = false`, because nothing here was suggested.
 */
@HiltViewModel
class SubscriptionsViewModel @Inject constructor(
    videos: VideoFeedRepository,
    private val urlResolver: MediaUrlResolver,
    private val queue: TubeQueue,
    private val follows: FollowGraph,
    engagement: EngagementStore,
    hidden: HiddenPosts,
) : ViewModel() {

    val items: Flow<PagingData<FeedItem>> = videos.videos(VideoFeedQuery.Following)
        .cachedIn(viewModelScope)
        .combine(hidden.state) { page, set ->
            if (set.isEmpty) page else page.filter { !set.hides(it) }
        }

    val overlays: StateFlow<Map<String, EngagementOverlay>> = engagement.overlays
    val followEdges: StateFlow<Map<String, FollowStatus>> = follows.edges
    val ownUserId: String get() = follows.ownId

    fun thumb(item: FeedItem): VideoThumb = urlResolver.videoThumb(item)

    /** A card was tapped: the loaded rows become the watch screen's queue. */
    fun onOpen(loaded: List<FeedItem>) = queue.set(loaded)
}

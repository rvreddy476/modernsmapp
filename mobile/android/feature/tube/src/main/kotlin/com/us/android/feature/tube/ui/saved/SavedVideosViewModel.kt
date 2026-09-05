package com.us.android.feature.tube.ui.saved

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.paging.PagingData
import androidx.paging.cachedIn
import androidx.paging.filter
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.engagement.data.EngagementStore
import com.us.android.core.engagement.data.HiddenPosts
import com.us.android.core.feed.data.FollowGraph
import com.us.android.core.feed.data.VideoFeedRepository
import com.us.android.core.feed.data.VideoThumb
import com.us.android.core.feed.data.hides
import com.us.android.core.feed.data.videoThumb
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FollowStatus
import com.us.android.feature.tube.data.TubeQueue
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import javax.inject.Inject

/**
 * The saved videos page (header More, 2026-09-05): the viewer's bookmarks
 * narrowed to long videos — the same paged read the You page's Saved
 * section makes, on a page of its own so the whole list can be scrolled.
 * The same "more" sheet with `suggested = false`: the viewer chose these.
 */
@HiltViewModel
class SavedVideosViewModel @Inject constructor(
    videos: VideoFeedRepository,
    private val urlResolver: MediaUrlResolver,
    private val queue: TubeQueue,
    private val follows: FollowGraph,
    engagement: EngagementStore,
    hidden: HiddenPosts,
) : ViewModel() {

    val items: Flow<PagingData<FeedItem>> = videos.savedVideos()
        .cachedIn(viewModelScope)
        .combine(hidden.state) { page, set ->
            // Unsave from the more sheet removes the row at once.
            if (set.isEmpty) page else page.filter { !set.hides(it) }
        }

    val overlays: StateFlow<Map<String, EngagementOverlay>> = engagement.overlays
    val followEdges: StateFlow<Map<String, FollowStatus>> = follows.edges
    val ownUserId: String get() = follows.ownId

    fun thumb(item: FeedItem): VideoThumb = urlResolver.videoThumb(item)

    /** A card was tapped: the loaded rows become the watch screen's queue. */
    fun onOpen(loaded: List<FeedItem>) = queue.set(loaded)
}

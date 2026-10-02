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
import com.us.android.core.feed.data.SavedKind
import com.us.android.core.feed.data.VideoFeedRepository
import com.us.android.core.feed.data.VideoThumb
import com.us.android.core.feed.data.hides
import com.us.android.core.feed.data.savedKind
import com.us.android.core.feed.data.videoThumb
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.media.ReelsEntry
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FollowStatus
import com.us.android.feature.tube.data.TubeQueue
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import javax.inject.Inject

/**
 * The Saved page (header More, 2026-09-05): what the viewer bookmarked that
 * plays. The same "more" sheet with `suggested = false`: the viewer chose
 * these.
 *
 * 2026-10-02: reels are listed too. The page used to narrow the bookmarks to
 * long videos, and no other screen lists bookmarks, so a reel saved from the
 * rail could not be found again anywhere in the app. A long video opens the
 * watch screen; a reel opens the Reels tab on that reel.
 */
@HiltViewModel
// Constructor injection of the page's collaborators; a wrapper would add
// indirection, not clarity.
@Suppress("LongParameterList")
class SavedVideosViewModel @Inject constructor(
    videos: VideoFeedRepository,
    private val urlResolver: MediaUrlResolver,
    private val queue: TubeQueue,
    private val reelsEntry: ReelsEntry,
    private val follows: FollowGraph,
    engagement: EngagementStore,
    hidden: HiddenPosts,
) : ViewModel() {

    val overlays: StateFlow<Map<String, EngagementOverlay>> = engagement.overlays

    val items: Flow<PagingData<FeedItem>> = combine(
        videos.savedVideosAndReels().cachedIn(viewModelScope),
        hidden.state,
        overlays,
    ) { page, set, taps ->
        page.filter { item -> !set.hides(item) && stillSaved(taps[item.id]) }
    }

    val followEdges: StateFlow<Map<String, FollowStatus>> = follows.edges
    val ownUserId: String get() = follows.ownId

    fun thumb(item: FeedItem): VideoThumb = urlResolver.videoThumb(item)

    /**
     * A card was tapped. A long video takes the loaded long videos with it
     * as the watch screen's queue; a reel leaves its id where Reels reads it.
     * Answers which of the two the caller should navigate to.
     */
    fun onOpen(item: FeedItem, loaded: List<FeedItem>): SavedKind {
        val kind = savedKind(item.feedContentType) ?: SavedKind.VIDEO
        when (kind) {
            SavedKind.VIDEO -> queue.set(loaded.filter { savedKind(it.feedContentType) == SavedKind.VIDEO })
            SavedKind.REEL -> reelsEntry.open(item.id)
        }
        return kind
    }
}

/**
 * Whether a saved row is still saved: gone the moment the viewer un-saves it
 * this session (from the sheet here, the watch screen or the reel's rail),
 * back if that un-save is refused. No local tap means the list's own word.
 */
fun stillSaved(overlay: EngagementOverlay?): Boolean = overlay?.bookmarked != false

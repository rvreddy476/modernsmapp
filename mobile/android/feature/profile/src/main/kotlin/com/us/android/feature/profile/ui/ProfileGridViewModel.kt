package com.us.android.feature.profile.ui

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.paging.PagingData
import androidx.paging.cachedIn
import com.us.android.core.feed.data.FollowGraph
import com.us.android.core.feed.data.VideoFeedRepository
import com.us.android.core.feed.data.VideoThumb
import com.us.android.core.feed.data.videoThumb
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.media.publish.ReelPublishActions
import com.us.android.core.media.publish.ReelPublishState
import com.us.android.core.media.publish.ReelPublishTracker
import com.us.android.core.media.publish.VideoKind
import com.us.android.core.model.FeedItem
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import javax.inject.Inject

/** The three tabs of a profile's media grid, in the order they are drawn. */
enum class ProfileGridTab(val label: String, val contentType: String) {
    POSTS("Posts", "post"),
    REELS("Reels", "flick"),
    VIDEOS("Videos", "long_video"),
}

/**
 * The video the viewer is posting, as the first tile of their own Videos
 * tab (founder, 2026-09-05): the chosen cover with a ring in the middle —
 * a sweep while the bytes go up, a spin while the server works — and,
 * when it stops, the reason with Retry / Discard (or "Create channel").
 */
data class PendingVideoTile(
    val creationKey: String,
    val coverPath: String?,
    val title: String,
    val state: ReelPublishState,
) {
    val failure: ReelPublishState.Failed? get() = state as? ReelPublishState.Failed
}

/**
 * Which tab a pending video belongs on, if any: a long video posts to
 * Videos; a reel keeps its place on the Reels tab (the app's, not the
 * profile's) and never shows here. Pure, so the placement is a table test.
 */
fun pendingTabFor(kind: VideoKind?): ProfileGridTab? = when (kind) {
    VideoKind.LONG -> ProfileGridTab.VIDEOS
    VideoKind.REEL, null -> null
}

/**
 * The profile's media grid (2026-09-05): a user's posts, reels and long
 * videos as three paged lists through the feed seam, and — on the viewer's
 * own profile — the video they are posting, first, until it is published.
 *
 * The publish tracker is process-wide; this ViewModel reads it only for a
 * LONG video and only on the own profile, and lets go of a finished
 * publish once the grid has refreshed to carry the real post.
 */
@HiltViewModel
class ProfileGridViewModel @Inject constructor(
    private val videos: VideoFeedRepository,
    private val urlResolver: MediaUrlResolver,
    private val tracker: ReelPublishTracker,
    private val publishActions: ReelPublishActions,
    follows: FollowGraph,
    savedStateHandle: SavedStateHandle,
) : ViewModel() {

    private val ownId: String = follows.ownId

    /** The profile's owner: the route's id, or the viewer on the Me tab. */
    val userId: String = savedStateHandle.get<String>(USER_ID_KEY) ?: ownId

    val isOwn: Boolean = userId.isNotBlank() && userId == ownId

    private val _tab = MutableStateFlow(ProfileGridTab.POSTS)
    val tab: StateFlow<ProfileGridTab> = _tab

    /** A nudge for the Videos list to reload once a pending video is published. */
    private val _reloadVideos = MutableStateFlow(0)
    val reloadVideos: StateFlow<Int> = _reloadVideos

    val posts: Flow<PagingData<FeedItem>> = paged(ProfileGridTab.POSTS)
    val reels: Flow<PagingData<FeedItem>> = paged(ProfileGridTab.REELS)
    val longVideos: Flow<PagingData<FeedItem>> = paged(ProfileGridTab.VIDEOS)

    /** The tile above the Videos tab while the viewer's own long video posts; null otherwise. */
    val pending: StateFlow<PendingVideoTile?> = combine(tracker.state, tracker.preview) { state, preview ->
        when {
            !isOwn || preview == null || pendingTabFor(preview.kind) == null -> null
            state is ReelPublishState.Idle -> null
            else -> PendingVideoTile(
                creationKey = preview.creationKey,
                coverPath = preview.coverPath,
                title = preview.title.ifBlank { preview.caption },
                state = state,
            )
        }
    }.stateIn(viewModelScope, SharingStarted.WhileSubscribed(STOP_TIMEOUT_MILLIS), null)

    init {
        // A pending video arriving switches the grid to where it will show.
        viewModelScope.launch {
            pending.collect { tile -> if (tile != null) _tab.value = ProfileGridTab.VIDEOS }
        }
        // Published: reload the Videos list so the real post lands, then let
        // the tracker go so the pending tile leaves.
        viewModelScope.launch {
            tracker.state.collect { state ->
                val ours = isOwn && pendingTabFor(tracker.preview.value?.kind) != null
                if (state is ReelPublishState.Published && ours) {
                    _reloadVideos.value += 1
                    publishActions.dismiss()
                }
            }
        }
    }

    fun select(tab: ProfileGridTab) {
        _tab.value = tab
    }

    fun retryPublish() = publishActions.retry()

    fun discardPublish() = publishActions.discard()

    /** What a tile draws: the cover or the still, the wash, and the length for a video. */
    fun thumb(item: FeedItem): VideoThumb = urlResolver.videoThumb(item)

    private fun paged(tab: ProfileGridTab): Flow<PagingData<FeedItem>> =
        videos.authorPosts(userId, tab.contentType).cachedIn(viewModelScope)

    private companion object {
        const val USER_ID_KEY = "userId"
        const val STOP_TIMEOUT_MILLIS = 5_000L
    }
}

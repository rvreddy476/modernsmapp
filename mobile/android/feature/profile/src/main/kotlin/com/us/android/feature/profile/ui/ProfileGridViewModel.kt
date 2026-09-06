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
import com.us.android.core.media.FeedEntry
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.media.ReelsEntry
import com.us.android.core.media.publish.PublishKind
import com.us.android.core.media.publish.PublishSchedule
import com.us.android.core.media.publish.ReelPublishActions
import com.us.android.core.media.publish.ReelPublishItem
import com.us.android.core.media.publish.ReelPublishState
import com.us.android.core.media.publish.ReelPublishTracker
import com.us.android.core.model.FeedItem
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** The three tabs of a profile's media grid, in the order they are drawn. */
enum class ProfileGridTab(val label: String, val contentType: String) {
    POSTS("Posts", "post"),
    REELS("Reels", "flick"),
    VIDEOS("Videos", "long_video"),
}

/**
 * A video the viewer is posting, as a tile at the head of its tab
 * (founder, 2026-09-05): the chosen cover with a ring in the middle — a
 * sweep and the percent while the bytes go up, a spin while the server
 * works — a clock when it is scheduled, and, when it stops, the reason
 * with Retry / Discard (or "Create channel").
 */
data class PendingVideoTile(
    val creationKey: String,
    val coverPath: String?,
    val title: String,
    val state: ReelPublishState,
    val tab: ProfileGridTab,
    /** RFC 3339 when the post is scheduled; the tile wears a clock. */
    val publishAt: String? = null,
) {
    val failure: ReelPublishState.Failed? get() = state as? ReelPublishState.Failed

    /** "Scheduled · 6 Sep 18:30", when there is a schedule to say. */
    val scheduleLabel: String? get() = PublishSchedule.tileLabel(publishAt)
}

/** A post the server is holding until its `publish_at` (2026-09-05): drawn with a clock until then. */
data class ScheduledTile(val item: FeedItem, val tab: ProfileGridTab) {
    val label: String get() = PublishSchedule.tileLabel(item.publishAt) ?: "Scheduled"
}

/**
 * Which tab a pending publish belongs on: a long video posts to Videos; a
 * reel — since the studio (2026-09-05) lands on the own profile — to
 * Reels; a photo post to Posts, since the photo studio now lands here too
 * (founder, 2026-09-06). Pure, so the placement is a table test.
 */
fun pendingTabFor(kind: PublishKind?): ProfileGridTab? = when (kind) {
    PublishKind.LONG -> ProfileGridTab.VIDEOS
    PublishKind.REEL -> ProfileGridTab.REELS
    PublishKind.PHOTO -> ProfileGridTab.POSTS
    null -> null
}

/** Which tab a scheduled post belongs on, by its content type; null for a kind the grid has no tab for. */
fun scheduledTabFor(contentType: String): ProfileGridTab? =
    ProfileGridTab.entries.firstOrNull { it.contentType == contentType }

/**
 * The profile's media grid (2026-09-05): a user's posts, reels and long
 * videos as three paged lists through the feed seam, and — on the viewer's
 * own profile — the videos they are posting, first, each with its own
 * ring, then the posts the server is holding for a later `publish_at`.
 *
 * The publish tracker is process-wide and holds a QUEUE; this ViewModel
 * reads it only on the own profile, and lets go of each finished publish
 * once its tab has refreshed to carry the real post.
 */
@HiltViewModel
// Constructor injection of the grid's collaborators: three paged lists, the
// publish queue, and the two tab-root hand-offs. A wrapper would add
// indirection, not clarity.
@Suppress("LongParameterList")
class ProfileGridViewModel @Inject constructor(
    private val videos: VideoFeedRepository,
    private val urlResolver: MediaUrlResolver,
    private val tracker: ReelPublishTracker,
    private val publishActions: ReelPublishActions,
    private val feedEntry: FeedEntry,
    private val reelsEntry: ReelsEntry,
    follows: FollowGraph,
    savedStateHandle: SavedStateHandle,
) : ViewModel() {

    private val ownId: String = follows.ownId

    /** The profile's owner: the route's id, or the viewer on the Me tab. */
    val userId: String = savedStateHandle.get<String>(USER_ID_KEY) ?: ownId

    val isOwn: Boolean = userId.isNotBlank() && userId == ownId

    private val _tab = MutableStateFlow(ProfileGridTab.POSTS)
    val tab: StateFlow<ProfileGridTab> = _tab

    /** A nudge, per tab, for its list to reload once a pending post is published. */
    private val _reloads = MutableStateFlow<Map<ProfileGridTab, Int>>(emptyMap())
    val reloads: StateFlow<Map<ProfileGridTab, Int>> = _reloads

    /**
     * "Your post is live, here is where it lives." One event per finished
     * publish, replayed to nobody: a buffered SharedFlow rather than a state,
     * because arriving at the feed is something that HAPPENS ONCE — a state
     * would fire again on every recomposition and every return to the profile.
     */
    private val _published = MutableSharedFlow<ProfileGridTab>(extraBufferCapacity = PUBLISHED_BUFFER)
    val published: SharedFlow<ProfileGridTab> = _published.asSharedFlow()

    val posts: Flow<PagingData<FeedItem>> = paged(ProfileGridTab.POSTS)
    val reels: Flow<PagingData<FeedItem>> = paged(ProfileGridTab.REELS)
    val longVideos: Flow<PagingData<FeedItem>> = paged(ProfileGridTab.VIDEOS)

    /** The viewer's own pending videos, oldest first, each on its tab; empty on anyone else's profile. */
    val pending: StateFlow<List<PendingVideoTile>> = tracker.items
        .map { items -> if (isOwn) items.mapNotNull { it.toTile() } else emptyList() }
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(STOP_TIMEOUT_MILLIS), emptyList())

    private val _scheduled = MutableStateFlow<List<ScheduledTile>>(emptyList())

    /** The viewer's own scheduled posts, soonest first; empty on anyone else's profile. */
    val scheduled: StateFlow<List<ScheduledTile>> = _scheduled

    private var seenKeys: Set<String> = emptySet()

    init {
        if (isOwn) {
            refreshScheduled()
            viewModelScope.launch { tracker.items.collect { items -> onQueueChanged(items) } }
        }
    }

    /**
     * A pending publish arriving switches the grid to where it will show.
     * Published: reload that tab so the real post lands (or the scheduled
     * list, when it was scheduled), ASK THE SHELL TO CARRY THE VIEWER ON to
     * where the post now lives, then let the tracker go so the pending tile
     * leaves.
     *
     * The hand-off is an event rather than a read of the tracker by the
     * destination, because this ViewModel dismisses the tracker entry on the
     * same pass — a destination that is not composed yet would find nothing
     * left to read. The post id travels in [FeedEntry] / [ReelsEntry] for the
     * same reason the feed-to-Reels tap does: a tab root is restored, not
     * pushed, so the navigation itself carries no argument.
     *
     * A SCHEDULED post is not carried anywhere. It does not exist on any feed
     * yet, so the viewer stays on the profile where its clock tile is.
     */
    private fun onQueueChanged(items: List<ReelPublishItem>) {
        val tiles = items.mapNotNull { it.toTile() }
        val fresh = tiles.filter { it.creationKey !in seenKeys }
        seenKeys = tiles.mapTo(mutableSetOf()) { it.creationKey }
        fresh.lastOrNull()?.let { _tab.value = it.tab }
        items.filter { it.state is ReelPublishState.Published }.forEach { item ->
            val postId = (item.state as ReelPublishState.Published).postId
            item.toTile()?.let { tile ->
                if (tile.publishAt != null) {
                    refreshScheduled()
                } else {
                    reload(tile.tab)
                    handOff(tile.tab, postId)
                }
            }
            publishActions.dismiss(item.creationKey)
        }
    }

    /**
     * Where a finished publish takes the viewer.
     *
     * Posts and reels have a feed to land on, so the post id is pinned there
     * and the shell is asked to switch tabs. A LONG video has no such tab in
     * this app — Tube is its home and the profile's Videos tab already shows
     * it — so it stays put rather than being sent somewhere arbitrary.
     */
    private fun handOff(tab: ProfileGridTab, postId: String) {
        if (postId.isBlank()) return
        when (tab) {
            ProfileGridTab.POSTS -> feedEntry.showFirst(postId)
            ProfileGridTab.REELS -> reelsEntry.open(postId)
            ProfileGridTab.VIDEOS -> return
        }
        _published.tryEmit(tab)
    }

    private fun reload(tab: ProfileGridTab) = _reloads.update { map -> map + (tab to (map[tab] ?: 0) + 1) }

    /** The scheduled list, again — after a schedule lands, or on pull. */
    fun refreshScheduled() {
        viewModelScope.launch {
            _scheduled.value = videos.scheduledPosts(SCHEDULED_LIMIT)
                .filter { it.isScheduled || !it.publishAt.isNullOrBlank() }
                .mapNotNull { item -> scheduledTabFor(item.feedContentType)?.let { ScheduledTile(item, it) } }
        }
    }

    fun select(tab: ProfileGridTab) {
        _tab.value = tab
    }

    fun retryPublish(creationKey: String) = publishActions.retry(creationKey)

    fun discardPublish(creationKey: String) = publishActions.discard(creationKey)

    /** What a tile draws: the cover or the still, the wash, and the length for a video. */
    fun thumb(item: FeedItem): VideoThumb = urlResolver.videoThumb(item)

    private fun ReelPublishItem.toTile(): PendingVideoTile? {
        val preview = preview ?: return null
        if (state is ReelPublishState.Idle) return null
        val tab = pendingTabFor(preview.kind) ?: return null
        return PendingVideoTile(
            creationKey = creationKey,
            coverPath = preview.coverPath,
            title = preview.title.ifBlank { preview.caption },
            state = state,
            tab = tab,
            publishAt = preview.publishAt,
        )
    }

    private fun paged(tab: ProfileGridTab): Flow<PagingData<FeedItem>> =
        videos.authorPosts(userId, tab.contentType).cachedIn(viewModelScope)

    private companion object {
        const val USER_ID_KEY = "userId"
        const val STOP_TIMEOUT_MILLIS = 5_000L
        const val SCHEDULED_LIMIT = 50

        /** Room for a queue that finishes several publishes while the screen is away. */
        const val PUBLISHED_BUFFER = 4
    }
}
